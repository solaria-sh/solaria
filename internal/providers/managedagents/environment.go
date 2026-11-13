package managedagents

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/anthropics/anthropic-sdk-go/v2"
	"github.com/anthropics/anthropic-sdk-go/v2/packages/param"

	"github.com/solaria-sh/solaria/internal/api/v1alpha1"
	"github.com/solaria-sh/solaria/internal/diag"
	"github.com/solaria-sh/solaria/internal/digest"
	"github.com/solaria-sh/solaria/internal/provider"
	"github.com/solaria-sh/solaria/internal/providers/container"
	"github.com/solaria-sh/solaria/internal/resolve"
	"github.com/solaria-sh/solaria/internal/secrets"
	"github.com/solaria-sh/solaria/internal/state"
)

// The self-hosted worker's source, built into its image. go.mod and go.sum
// carry a .txt suffix so the Go tool doesn't take the folder for a module
// of its own.
var (
	//go:embed worker/main.go
	workerMain []byte
	//go:embed worker/go.mod.txt
	workerGoMod []byte
	//go:embed worker/go.sum.txt
	workerGoSum []byte
)

// golangImage compiles the worker. It runs on the build machine's platform
// and cross-compiles for the worker's.
const golangImage = "golang:1.27.3-bookworm"

// The cloud sandbox's fixed limits (ADR 0007).
const (
	cloudMemory = 8 << 30
	cloudDisk   = 10 << 30
)

// Environment binding outputs.
const (
	outWorkerImage     = "workerImage"
	outWorkerContainer = "workerContainer"
	outSandboxImage    = "sandboxImage"
)

const labelPrefix = "sh.solaria."

// ValidateEnvironment reports what the host can't take from one of the
// target's Environments, once for all the agents it's given to. The
// engine drops the problems every Environment repeats, such as settings.
func (p *Provider) ValidateEnvironment(_ context.Context, e *provider.Environment) []diag.Diagnostic {
	_, diags := readSettings(e.Spec, e.Spec.Placement)
	errorf := func(format string, args ...any) {
		diags = append(diags, diag.Errorf("", diag.Pos{}, "Environment %q: %s", e.Name, fmt.Sprintf(format, args...)))
	}
	warnf := func(format string, args ...any) {
		diags = append(diags, diag.Warnf("", diag.Pos{}, "Environment %q: %s", e.Name, fmt.Sprintf(format, args...)))
	}
	sb := e.Sandbox
	limited := sb == nil || sb.Network == nil || sb.Network.Mode != v1alpha1.NetworkUnrestricted
	if e.Spec.Telemetry != nil {
		diags = append(diags, diag.Warnf("", diag.Pos{}, "telemetry is ignored: Managed Agents doesn't export to an OTLP endpoint; its sessions are traced in the Console"))
	}

	if e.Spec.Placement == PlacementSelfHosted {
		if limited {
			warnf("network is limited, but docker doesn't enforce host lists yet: the worker container can reach any host")
		}
		if sb != nil && sb.Resources != nil && sb.Resources.Disk != "" {
			warnf("spec.resources.disk isn't enforced on docker")
		}
		for _, pkg := range container.OptionLikePackages(sb) {
			errorf("package %q starts with '-'; package managers would read it as an option", pkg)
		}
		return diags
	}

	if pk := container.Packages(sb); pk != nil && hasPackages(pk) && limited && (sb.Network == nil || !sb.Network.PackageManagers) {
		errorf("spec.packages need spec.network.packageManagers: true under limited networking, or the cloud sandbox can't install them")
	}
	if sb != nil && sb.Resources != nil {
		for _, r := range []struct {
			field, value string
			limit        int64
		}{{"memory", sb.Resources.Memory, cloudMemory}, {"disk", sb.Resources.Disk, cloudDisk}} {
			if r.value == "" {
				continue
			}
			if n, ok := parseSize(r.value); ok && n > r.limit {
				errorf("spec.resources.%s is %s, above the cloud sandbox's fixed %dGi", r.field, r.value, r.limit>>30)
			} else {
				warnf("spec.resources.%s is ignored: the cloud sandbox has fixed limits of 8Gi memory and 10Gi disk", r.field)
			}
		}
	}
	if limited {
		var web []string
		for _, a := range e.Agents {
			if slices.ContainsFunc(a.Definition.Tools.Builtin, func(t string) bool { return t == "web_fetch" || t == "web_search" }) {
				web = append(web, a.Name)
			}
		}
		if len(web) > 0 {
			warnf("network is limited, so web_fetch and web_search reach only spec.network.allow hosts (used by %s)", strings.Join(web, ", "))
		}
	}
	return diags
}

// PlanEnvironment compares the environment's request, and on self-hosted
// its worker, with what was applied. A self-hosted worker that isn't
// running plans an update, which starts it.
func (p *Provider) PlanEnvironment(ctx context.Context, e *provider.Environment, prior *state.EnvironmentEntry) (*provider.EnvironmentPlan, error) {
	if e == nil {
		return provider.DefaultEnvironmentPlan("", nil, "", nil, prior), nil
	}
	cfg, err := p.environmentConfig(e)
	if err != nil {
		return nil, err
	}
	dg, err := digest.Of(cfg)
	if err != nil {
		return nil, err
	}
	pl := provider.DefaultEnvironmentPlan("", e, dg, cfg, prior)
	if e.Spec.Placement != PlacementSelfHosted {
		return pl, nil
	}

	s, _ := readSettings(e.Spec, e.Spec.Placement)
	src := secrets.For(e.Spec.Secrets, e.Dir)
	if _, ok, err := src.Lookup(keySecret(e, s)); err != nil {
		return nil, err
	} else if !ok {
		pl.Notes = append(pl.Notes, fmt.Sprintf("the worker needs the environment's key in %s as %s; apply creates the environment and then says how to get one", src.Describe, keySecret(e, s)))
	}
	if pl.Action == provider.NoOp {
		c, err := p.docker.InspectContainer(ctx, workerName(e))
		if err != nil {
			return nil, err
		}
		if c == nil || !c.Running {
			pl.Action = provider.Update
			pl.Reason = "the worker container isn't running"
		}
	}
	return pl, nil
}

// ApplyEnvironment creates or updates the Managed Agents environment, then,
// on self-hosted, builds and starts its worker. When the worker can't start
// yet, the environment is returned along with the error so it's recorded.
func (p *Provider) ApplyEnvironment(ctx context.Context, pl *provider.EnvironmentPlan) (*provider.Applied, error) {
	e := pl.Desired
	s, _ := readSettings(e.Spec, e.Spec.Placement)
	name := s.namePrefix + hostName(e)
	var env *anthropic.BetaEnvironment
	var err error
	if pl.Action == provider.Update {
		id := pl.Prior.Binding.HostID
		p.logf("Updating environment %s (%s)\n", name, id)
		env, err = p.client.Beta.Environments.Update(ctx, id, anthropic.BetaEnvironmentUpdateParams{
			Name:        param.NewOpt(name),
			Description: param.NewOpt(description(e)),
			Config:      anthropic.BetaEnvironmentUpdateParamsConfigUnion{OfCloud: cloudConfig(e), OfSelfHosted: selfHostedConfig(e)},
			Metadata:    environmentMetadata(e),
		})
	} else {
		p.logf("Creating environment %s\n", name)
		env, err = p.client.Beta.Environments.New(ctx, anthropic.BetaEnvironmentNewParams{
			Name:        name,
			Description: param.NewOpt(description(e)),
			Config:      anthropic.BetaEnvironmentNewParamsConfigUnion{OfCloud: cloudConfig(e), OfSelfHosted: selfHostedConfig(e)},
			Metadata:    environmentMetadata(e),
		})
	}
	if err != nil {
		return nil, err
	}
	applied := &provider.Applied{
		Binding:    state.Binding{HostID: env.ID, Outputs: map[string]string{}},
		RemoteHash: env.UpdatedAt,
	}
	if e.Spec.Placement != PlacementSelfHosted {
		return applied, nil
	}
	if pl.Prior != nil {
		// The worker's name follows the target's default Environment, so a
		// change of default renames it; remove the old container.
		if old := pl.Prior.Binding.Outputs[outWorkerContainer]; old != "" && old != workerName(e) {
			p.logf("Removing the worker container %s\n", old)
			if err := p.docker.RemoveContainer(ctx, old); err != nil {
				return applied, err
			}
		}
	}
	return applied, p.startWorker(ctx, e, s, env.ID, applied.Binding.Outputs)
}

// ReadEnvironment gets the environment. An archived one counts as gone.
// Its update time is the remote hash; the worker is checked by
// PlanEnvironment.
func (p *Provider) ReadEnvironment(ctx context.Context, e *state.EnvironmentEntry) (*provider.Observed, error) {
	env, err := p.client.Beta.Environments.Get(ctx, e.Binding.HostID, anthropic.BetaEnvironmentGetParams{})
	if isStatus(err, http.StatusNotFound) {
		return &provider.Observed{}, nil
	}
	if err != nil {
		return nil, err
	}
	if env.ArchivedAt != "" {
		return &provider.Observed{}, nil
	}
	return &provider.Observed{Exists: true, RemoteHash: env.UpdatedAt}, nil
}

// DestroyEnvironment stops the worker, removes its images, and deletes the
// environment. One that sessions still refer to can't be deleted, so it's
// archived instead.
func (p *Provider) DestroyEnvironment(ctx context.Context, e *state.EnvironmentEntry) error {
	if e.Placement == PlacementSelfHosted {
		name := e.Binding.Outputs[outWorkerContainer]
		if name == "" {
			// The worker never started; remove one that might have.
			name = "solaria-" + p.env + "-worker"
			if n := strings.TrimPrefix(e.ID, v1alpha1.KindEnvironment+"/"); n != v1alpha1.DefaultEnvironment {
				name = "solaria-" + p.env + "-" + n + "-worker"
			}
		}
		p.logf("Removing the worker container %s\n", name)
		if err := p.docker.RemoveContainer(ctx, name); err != nil {
			return err
		}
		for _, k := range []string{outWorkerImage, outSandboxImage} {
			if ref := e.Binding.Outputs[k]; ref != "" {
				if err := p.docker.Remove(ctx, ref); err != nil {
					return err
				}
			}
		}
	}
	id := e.Binding.HostID
	p.logf("Deleting environment %s\n", id)
	_, err := p.client.Beta.Environments.Delete(ctx, id, anthropic.BetaEnvironmentDeleteParams{})
	switch {
	case err == nil, isStatus(err, http.StatusNotFound):
		return nil
	case isStatus(err, http.StatusBadRequest, http.StatusConflict):
		p.logf("Sessions still refer to %s; archiving it instead\n", id)
		_, aerr := p.client.Beta.Environments.Archive(ctx, id, anthropic.BetaEnvironmentArchiveParams{})
		if aerr == nil || isArchived(aerr) {
			return nil
		}
		return errors.Join(err, aerr)
	default:
		return err
	}
}

// startWorker builds the worker image and (re)starts its container, which
// needs the environment's key.
func (p *Provider) startWorker(ctx context.Context, e *provider.Environment, s settings, envID string, outputs map[string]string) error {
	src := secrets.For(e.Spec.Secrets, e.Dir)
	key, ok, err := src.Lookup(keySecret(e, s))
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("environment %s (%s) exists, but its worker can't start without the environment's key: "+
			"generate one in the Claude Console, under Managed Agents > Environments > %s, store it in %s as %s, and run apply again",
			s.namePrefix+hostName(e), envID, s.namePrefix+hostName(e), src.Describe, keySecret(e, s))
	}

	w, err := renderWorker(e, s)
	if err != nil {
		return err
	}
	dir := filepath.Join(p.workDir, ".solaria", Name, hostName(e), "worker")
	if err := writeWorkerContext(dir, w.files); err != nil {
		return fmt.Errorf("write the worker's build context: %w", err)
	}
	if w.base != nil {
		p.logf("Building the sandbox image %s from %s\n", w.base.Tag, w.base.Dockerfile)
		if err := p.docker.Build(ctx, *w.base, p.log); err != nil {
			return err
		}
		outputs[outSandboxImage] = w.base.Tag
	}
	p.logf("Building the worker image %s for %s\n", w.image, s.platform)
	err = p.docker.Build(ctx, container.BuildRequest{
		Context:    dir,
		Dockerfile: filepath.Join(dir, "Dockerfile"),
		Tag:        w.image,
		Platform:   s.platform,
		Labels:     w.labels,
	}, p.log)
	if err != nil {
		return err
	}
	outputs[outWorkerImage] = w.image

	name := workerName(e)
	p.logf("Starting the worker container %s\n", name)
	if err := p.docker.RemoveContainer(ctx, name); err != nil {
		return err
	}
	_, err = p.docker.RunDetached(ctx, container.RunRequest{
		Name:     name,
		Image:    w.image,
		Platform: s.platform,
		Memory:   container.Memory(e.Sandbox),
		Env:      map[string]string{"ANTHROPIC_ENVIRONMENT_ID": envID},
		Secrets:  map[string]string{"ANTHROPIC_ENVIRONMENT_KEY": key},
		Labels:   w.labels,
	})
	if err != nil {
		return err
	}
	outputs[outWorkerContainer] = name
	return nil
}

// worker is the self-hosted worker's rendered build.
type worker struct {
	files  map[string][]byte
	image  string
	base   *container.BuildRequest
	labels map[string]string
}

// renderWorker writes the worker image's build context: the Solaria
// sandbox stages with the environment's packages, plus the worker binary.
func renderWorker(e *provider.Environment, s settings) (*worker, error) {
	labels := map[string]string{
		labelPrefix + "target":      e.Target,
		labelPrefix + "environment": e.Name,
		labelPrefix + "object":      e.ID(),
	}
	base, build, err := container.SandboxBase(e.Sandbox, e.SandboxDir, "solaria/"+hostName(e)+"/worker-sandbox", s.platform, labels)
	if err != nil {
		return nil, err
	}
	var b strings.Builder
	b.WriteString("# syntax=docker/dockerfile:1\n")
	b.WriteString("# Generated by Solaria: the Managed Agents self-hosted worker for Environment " + e.Name + " of target " + e.Target + ".\n\n")
	b.WriteString("# The worker, cross-compiled on the build machine.\n")
	fmt.Fprintf(&b, "FROM --platform=$BUILDPLATFORM %s AS worker\n", golangImage)
	b.WriteString("ARG TARGETOS TARGETARCH\n")
	b.WriteString("WORKDIR /src\n")
	b.WriteString("COPY go.mod go.sum main.go ./\n")
	b.WriteString("RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -o /solaria-worker .\n\n")
	container.WriteSandboxStages(&b, base, container.Packages(e.Sandbox))
	b.WriteString("# Tools run in /workspace; memory stores mount under /mnt/memory.\n")
	b.WriteString("FROM sandbox\n")
	b.WriteString("COPY --from=worker /solaria-worker /usr/local/bin/solaria-worker\n")
	b.WriteString("RUN mkdir -p /workspace /mnt/memory\n")
	b.WriteString("WORKDIR /workspace\n")
	b.WriteString(`ENTRYPOINT ["solaria-worker"]` + "\n")

	files := map[string][]byte{
		"Dockerfile": []byte(b.String()),
		"main.go":    workerMain,
		"go.mod":     workerGoMod,
		"go.sum":     workerGoSum,
	}
	dg, err := digest.Of(files)
	if err != nil {
		return nil, err
	}
	labels[labelPrefix+"build-digest"] = dg
	return &worker{
		files:  files,
		image:  "solaria/" + hostName(e) + "/worker:" + strings.TrimPrefix(dg, "sha256:")[:12],
		base:   build,
		labels: labels,
	}, nil
}

func writeWorkerContext(dir string, files map[string][]byte) error {
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), content, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// environmentConfig is what PlanEnvironment records and diffs: the request
// and, on self-hosted, the worker's inputs.
func (p *Provider) environmentConfig(e *provider.Environment) (map[string]any, error) {
	s, _ := readSettings(e.Spec, e.Spec.Placement)
	cfg := map[string]any{
		"name":        s.namePrefix + hostName(e),
		"description": description(e),
		"metadata":    environmentMetadata(e),
	}
	if c := cloudConfig(e); c != nil {
		pk := c.Packages
		net := map[string]any{"type": "unrestricted"}
		if l := c.Networking.OfLimited; l != nil {
			net = map[string]any{
				"type":                   "limited",
				"allowed_hosts":          l.AllowedHosts,
				"allow_package_managers": l.AllowPackageManagers.Value,
				"allow_mcp_servers":      l.AllowMCPServers.Value,
			}
		}
		cfg["config"] = map[string]any{
			"type":       "cloud",
			"packages":   map[string]any{"apt": pk.Apt, "cargo": pk.Cargo, "gem": pk.Gem, "go": pk.Go, "npm": pk.Npm, "pip": pk.Pip},
			"networking": net,
		}
		return cfg, nil
	}
	cfg["config"] = map[string]any{"type": "self_hosted"}
	w, err := renderWorker(e, s)
	if err != nil {
		return nil, err
	}
	cfg["worker"] = map[string]any{
		"image":     w.image,
		"platform":  s.platform,
		"memory":    container.Memory(e.Sandbox),
		"keySecret": keySecret(e, s),
	}
	if w.base != nil {
		cfg["worker"].(map[string]any)["sandboxImage"] = w.base.Tag
	}
	return cfg, nil
}

// cloudConfig is the cloud request's config, or nil on self-hosted. Every
// list is sent, empty ones too, so an update clears what the card dropped.
func cloudConfig(e *provider.Environment) *anthropic.BetaCloudConfigParams {
	if e.Spec.Placement == PlacementSelfHosted {
		return nil
	}
	sb := e.Sandbox
	pk := container.Packages(sb)
	if pk == nil {
		pk = &v1alpha1.PackagesSpec{}
	}
	c := &anthropic.BetaCloudConfigParams{Packages: anthropic.BetaPackagesParams{
		Apt: list(pk.APT), Cargo: list(pk.Cargo), Gem: list(pk.Gem), Go: list(pk.Go), Npm: list(pk.NPM), Pip: list(pk.Pip),
		Type: anthropic.BetaPackagesParamsTypePackages,
	}}
	if sb != nil && sb.Network != nil && sb.Network.Mode == v1alpha1.NetworkUnrestricted {
		c.Networking.OfUnrestricted = &anthropic.BetaUnrestrictedNetworkParam{}
		return c
	}
	// An omitted network is limited, never the host's unrestricted default
	// (ADR 0007).
	l := &anthropic.BetaLimitedNetworkParams{AllowedHosts: []string{}}
	if sb != nil && sb.Network != nil {
		l.AllowedHosts = list(sb.Network.Allow)
		l.AllowPackageManagers = param.NewOpt(sb.Network.PackageManagers)
	} else {
		l.AllowPackageManagers = param.NewOpt(false)
	}
	l.AllowMCPServers = param.NewOpt(slices.ContainsFunc(e.Agents, func(a *resolve.Agent) bool { return len(a.Definition.Tools.MCP) > 0 }))
	c.Networking.OfLimited = l
	return c
}

func selfHostedConfig(e *provider.Environment) *anthropic.BetaSelfHostedConfigParams {
	if e.Spec.Placement != PlacementSelfHosted {
		return nil
	}
	c := anthropic.NewBetaSelfHostedConfigParams()
	return &c
}

func description(e *provider.Environment) string {
	if e.Name == v1alpha1.DefaultEnvironment {
		return "Solaria target " + e.Target + ", built-in sandbox"
	}
	return "Solaria target " + e.Target + ", Environment " + e.Name
}

func environmentMetadata(e *provider.Environment) map[string]string {
	return map[string]string{metaTarget: e.Target, metaEnvironment: e.Name}
}

// hostName is what the host's objects for an Environment are named after:
// the target for its default Environment, which keeps the names it had
// before ADR 0020, and the target and Environment for the others.
func hostName(e *provider.Environment) string {
	if e.Name == resolve.DefaultEnvironment(e.Spec) {
		return e.Target
	}
	return e.Target + "-" + e.Name
}

// keySecret is the secret that holds a self-hosted Environment's key:
// environmentKeySecret for the target's default Environment, and with the
// Environment's name added, as in ANTHROPIC_ENVIRONMENT_KEY_DATA, for the
// others.
func keySecret(e *provider.Environment, s settings) string {
	if e.Name == resolve.DefaultEnvironment(e.Spec) {
		return s.keySecret
	}
	return s.keySecret + "_" + strings.ToUpper(strings.ReplaceAll(e.Name, "-", "_"))
}

// workerName names an Environment's worker container.
func workerName(e *provider.Environment) string { return "solaria-" + hostName(e) + "-worker" }

func hasPackages(p *v1alpha1.PackagesSpec) bool {
	return len(p.APT)+len(p.Cargo)+len(p.Gem)+len(p.Go)+len(p.NPM)+len(p.Pip) > 0
}

// list returns l, or an empty list that the request still sends.
func list(l []string) []string {
	if l == nil {
		return []string{}
	}
	return l
}

// parseSize reads a resources quantity such as 8Gi into bytes.
func parseSize(s string) (int64, bool) {
	shift := 0
	for i, suffix := range []string{"Ki", "Mi", "Gi", "Ti"} {
		if n, ok := strings.CutSuffix(s, suffix); ok {
			s, shift = n, 10*(i+1)
			break
		}
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n > (1<<62)>>shift {
		return 0, false
	}
	return n << shift, true
}
