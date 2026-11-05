// Package managedagents deploys agents to Claude Managed Agents, which runs
// the agent loop on Anthropic's side (ADR 0011). It calls the API through
// the Go SDK.
//
// Each Solaria environment becomes one Managed Agents environment, which
// all its agents share (ADR 0012):
//
//   - placement cloud: Anthropic runs the sandbox, from the environment's
//     packages and networking (ADR 0007);
//   - placement self-hosted: the sandbox is a worker container on this
//     machine. Apply builds it on the Solaria sandbox base and runs it,
//     once the environment's key, which only the Console can generate, is
//     in the secret source.
//
// Each agent becomes a Managed Agents agent, whose version increases with
// every change. Its skills are uploaded as custom skills and pinned by
// version. Destroy archives agents, which the host can't delete.
//
// Environments can set providers.managed-agents:
//
//   - namePrefix: put before every name Solaria gives a host object;
//   - environmentKeySecret: the secret holding a self-hosted environment's
//     key, ANTHROPIC_ENVIRONMENT_KEY by default;
//   - platform: the self-hosted worker image's platform, linux/amd64 by
//     default.
//
// Agents can set providers.managed-agents fields the card has no word for,
// such as multiagent; they are sent as they are.
package managedagents

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"mime"
	"net/http"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/anthropics/anthropic-sdk-go/v2"
	"github.com/anthropics/anthropic-sdk-go/v2/option"
	"github.com/anthropics/anthropic-sdk-go/v2/packages/param"

	"github.com/renantarouco/solaria/internal/api/v1alpha1"
	"github.com/renantarouco/solaria/internal/diag"
	"github.com/renantarouco/solaria/internal/digest"
	"github.com/renantarouco/solaria/internal/provider"
	"github.com/renantarouco/solaria/internal/providers/container"
	"github.com/renantarouco/solaria/internal/resolve"
	"github.com/renantarouco/solaria/internal/state"
	"github.com/renantarouco/solaria/internal/version"
)

// Name of the provider.
const Name = "managed-agents"

// Placements.
const (
	PlacementCloud      = "cloud"
	PlacementSelfHosted = "self-hosted"
)

// Agent binding outputs. Skills add skill.<name>.id, .version and .digest.
const (
	outRequestDigest = "requestDigest"
	outSkillPrefix   = "skill."
)

// Docker is the part of the docker CLI the self-hosted placement uses.
// Tests replace it.
type Docker interface {
	container.Docker
	RunDetached(ctx context.Context, req container.RunRequest) (string, error)
	InspectContainer(ctx context.Context, name string) (*container.Container, error)
	RemoveContainer(ctx context.Context, name string) error
}

// New returns the provider with a client configured from the environment:
// ANTHROPIC_API_KEY, or an ant CLI profile, as the SDK resolves them.
func New(opts provider.Options) (provider.Provider, error) {
	return NewWithClient(opts, anthropic.NewClient(), container.CLI{}), nil
}

// NewWithClient returns the provider with a given client and Docker, for
// tests.
func NewWithClient(opts provider.Options, client anthropic.Client, d Docker) *Provider {
	log := opts.Log
	if log == nil {
		log = io.Discard
	}
	return &Provider{env: opts.Environment, workDir: opts.WorkDir, log: log, client: client, docker: d}
}

// Provider is the managed-agents provider.
type Provider struct {
	env, workDir string
	log          io.Writer
	client       anthropic.Client
	docker       Docker
}

// Name returns managed-agents.
func (p *Provider) Name() string { return Name }

// Version is the Solaria version.
func (p *Provider) Version() string { return version.Version }

// Placements returns cloud and self-hosted.
func (p *Provider) Placements() []string { return []string{PlacementCloud, PlacementSelfHosted} }

// Capabilities of Managed Agents. It runs Claude models only, without
// fallback, and connects to remote MCP servers from Anthropic's side on both
// placements. Approvals are native: a tool under permissions.ask pauses the
// session until someone answers. Only the self-hosted worker can start from
// a custom image.
func (p *Provider) Capabilities(placement string) provider.Capabilities {
	return provider.Capabilities{
		ModelProviders: []string{"anthropic"},
		MCPTransports:  []string{provider.TransportStreamableHTTP, provider.TransportSSE},
		SandboxImage:   placement == PlacementSelfHosted,
		Approvals:      true,
		Destroy:        provider.DestroyArchive,
	}
}

// Validate reports what the host can't take from an agent. Environment-wide
// problems are reported by ValidateEnvironment.
func (p *Provider) Validate(_ context.Context, a *resolve.Agent) []diag.Diagnostic {
	s, _ := readSettings(a.Env, a.Placement)
	return validateAgent(a, s, fmt.Sprintf("%s on %s/%s", a.ID(), Name, a.Placement))
}

// Plan starts from the default plan, and also updates an agent whose
// request changed while its digests didn't, as when namePrefix changes.
func (p *Provider) Plan(_ context.Context, desired *resolve.Agent, prior *state.Entry) (*provider.Plan, error) {
	pl := provider.DefaultPlan(desired, prior)
	s, _ := readSettings(desired.Env, desired.Placement)
	reqDigest, err := requestDigest(renderAgent(desired, s, nil))
	if err != nil {
		return nil, err
	}
	switch pl.Action {
	case provider.NoOp:
		if old := prior.Binding.Outputs[outRequestDigest]; old != reqDigest {
			pl.Action = provider.Update
			pl.Reason = "the request to the host changed although the agent didn't"
			pl.Changes = []provider.Change{{Path: "request", Old: provider.ShortDigest(old), New: provider.ShortDigest(reqDigest)}}
		}
	case provider.Update:
		pl.Notes = append(pl.Notes, "creates a new agent version; sessions pinned to an earlier one keep it")
	}
	return pl, nil
}

// Apply uploads changed skills, creates or updates the agent, and deletes
// skills the agent no longer has.
func (p *Provider) Apply(ctx context.Context, pl *provider.Plan) (*provider.Applied, error) {
	a := pl.Desired
	s, _ := readSettings(a.Env, a.Placement)
	update := pl.Action == provider.Update
	// A create starts afresh even with a prior entry: the agent was
	// archived or deleted outside Solaria, or ran on another provider.
	var priorOut map[string]string
	if update {
		priorOut = pl.Prior.Binding.Outputs
	}
	outputs := map[string]string{}
	refs := map[string]skillRef{}
	for _, name := range slices.Sorted(maps.Keys(a.Definition.Skills)) {
		ref, dg, err := p.uploadSkill(ctx, a, s, name, priorOut)
		if err != nil {
			return nil, fmt.Errorf("skill %s: %w", name, err)
		}
		refs[name] = ref
		outputs[outSkillPrefix+name+".id"] = ref.ID
		outputs[outSkillPrefix+name+".version"] = ref.Version
		outputs[outSkillPrefix+name+".digest"] = dg
	}

	body := renderAgent(a, s, refs)
	opts := passthrough(body.Passthrough)
	var agent *anthropic.BetaManagedAgentsAgent
	var err error
	if update {
		id := pl.Prior.Binding.HostID
		p.logf("Updating agent %s (%s)\n", body.Name, id)
		agent, err = p.client.Beta.Agents.Update(ctx, id, body.updateParams(baseVersion(pl)), opts...)
		if isStatus(err, http.StatusConflict) {
			return nil, fmt.Errorf("%s changed on the host since it was planned; plan again, with refresh, to see how: %w", id, err)
		}
	} else {
		p.logf("Creating agent %s\n", body.Name)
		agent, err = p.client.Beta.Agents.New(ctx, body.newParams(), opts...)
	}
	if err != nil {
		return nil, err
	}

	// Skills the card dropped are deleted once no version of the agent
	// in use refers to them... which can't be known, so only the latest
	// version is considered: it no longer does.
	for _, name := range staleSkills(priorOut, a.Definition.Skills) {
		p.deleteSkill(ctx, priorOut[outSkillPrefix+name+".id"])
	}
	// The skills of an agent that disappeared outside Solaria are left
	// over; this provider uploaded them, so it cleans them up.
	if !update && pl.Prior != nil && pl.Prior.Provider == Name {
		for _, name := range staleSkills(pl.Prior.Binding.Outputs, nil) {
			p.deleteSkill(ctx, pl.Prior.Binding.Outputs[outSkillPrefix+name+".id"])
		}
	}

	reqDigest, err := requestDigest(renderAgent(a, s, nil))
	if err != nil {
		return nil, err
	}
	outputs[outRequestDigest] = reqDigest
	return &provider.Applied{
		Binding:    state.Binding{HostID: agent.ID, Version: strconv.FormatInt(agent.Version, 10), Outputs: outputs},
		RemoteHash: remoteHash(agent),
	}, nil
}

// Read gets the agent. An archived agent counts as gone: it can't be
// updated or used again. Its version is the remote hash, since every change
// on the host creates a version.
func (p *Provider) Read(ctx context.Context, e *state.Entry) (*provider.Observed, error) {
	agent, err := p.client.Beta.Agents.Get(ctx, e.Binding.HostID, anthropic.BetaAgentGetParams{})
	if isStatus(err, http.StatusNotFound) {
		return &provider.Observed{}, nil
	}
	if err != nil {
		return nil, err
	}
	if !agent.ArchivedAt.IsZero() {
		return &provider.Observed{}, nil
	}
	return &provider.Observed{Exists: true, RemoteHash: remoteHash(agent)}, nil
}

// Destroy archives the agent, which is irreversible, and deletes its
// skills. Sessions already running keep going.
func (p *Provider) Destroy(ctx context.Context, e *state.Entry) error {
	p.logf("Archiving agent %s\n", e.Binding.HostID)
	_, err := p.client.Beta.Agents.Archive(ctx, e.Binding.HostID, anthropic.BetaAgentArchiveParams{})
	if err != nil && !isStatus(err, http.StatusNotFound) && !isArchived(err) {
		return err
	}
	for _, name := range staleSkills(e.Binding.Outputs, nil) {
		p.deleteSkill(ctx, e.Binding.Outputs[outSkillPrefix+name+".id"])
	}
	return nil
}

// uploadSkill reuses the prior upload of an unchanged skill, adds a version
// to a changed one, and creates a new one.
func (p *Provider) uploadSkill(ctx context.Context, a *resolve.Agent, s settings, name string, prior map[string]string) (skillRef, string, error) {
	dg, err := digest.Of(a.Definition.Skills[name])
	if err != nil {
		return skillRef{}, "", err
	}
	id := prior[outSkillPrefix+name+".id"]
	if id != "" && prior[outSkillPrefix+name+".digest"] == dg {
		return skillRef{ID: id, Version: prior[outSkillPrefix+name+".version"]}, dg, nil
	}
	files, err := skillFiles(a, name)
	if err != nil {
		return skillRef{}, "", err
	}
	if id != "" {
		p.logf("Uploading a new version of skill %s (%s)\n", name, id)
		v, err := p.client.Skills.Versions.New(ctx, id, anthropic.SkillVersionNewParams{Files: files})
		if err == nil {
			return skillRef{ID: id, Version: v.ID}, dg, nil
		}
		if !isStatus(err, http.StatusNotFound) {
			return skillRef{}, "", err
		}
		// Deleted outside Solaria: upload it as a new skill.
		if files, err = skillFiles(a, name); err != nil {
			return skillRef{}, "", err
		}
	}
	p.logf("Uploading skill %s\n", name)
	sk, err := p.client.Skills.New(ctx, anthropic.SkillNewParams{Files: files, DisplayName: param.NewOpt(s.namePrefix + name)})
	if err != nil {
		return skillRef{}, "", err
	}
	return skillRef{ID: sk.ID, Version: sk.LatestVersionID}, dg, nil
}

// skillFiles reads a skill's files as the upload wants them: under one
// top-level folder named after the skill.
func skillFiles(a *resolve.Agent, name string) ([]io.Reader, error) {
	var files []io.Reader
	for _, rel := range slices.Sorted(maps.Keys(a.Definition.Skills[name].Files)) {
		content, err := resolve.ReadText(filepath.Join(a.SkillDirs[name], filepath.FromSlash(rel)))
		if err != nil {
			return nil, err
		}
		ctype := mime.TypeByExtension(path.Ext(rel))
		if ctype == "" || path.Ext(rel) == ".md" {
			ctype = "text/plain"
		}
		files = append(files, anthropic.File(bytes.NewReader(content), name+"/"+rel, ctype))
	}
	return files, nil
}

// deleteSkill deletes a skill and its versions. A skill that can't be
// deleted only takes space, so failing isn't an apply failure.
func (p *Provider) deleteSkill(ctx context.Context, id string) {
	if id == "" {
		return
	}
	p.logf("Deleting skill %s\n", id)
	pager := p.client.Skills.Versions.ListAutoPaging(ctx, id, anthropic.SkillVersionListParams{})
	var versions []string
	for pager.Next() {
		versions = append(versions, pager.Current().ID)
	}
	err := pager.Err()
	for _, v := range versions {
		if err != nil {
			break
		}
		if _, err = p.client.Skills.Versions.Delete(ctx, v, anthropic.SkillVersionDeleteParams{SkillID: id}); isStatus(err, http.StatusNotFound) {
			err = nil
		}
	}
	if err == nil {
		if _, err = p.client.Skills.Delete(ctx, id, anthropic.SkillDeleteParams{}); isStatus(err, http.StatusNotFound) {
			err = nil
		}
	}
	if err != nil {
		p.logf("warning: couldn't delete skill %s: %v\n", id, err)
	}
}

// staleSkills lists the skills in a binding's outputs that keep isn't
// holding, sorted.
func staleSkills(outputs map[string]string, keep map[string]resolve.Skill) []string {
	var out []string
	for k := range outputs {
		if name, ok := strings.CutSuffix(strings.TrimPrefix(k, outSkillPrefix), ".id"); ok && strings.HasPrefix(k, outSkillPrefix) {
			if _, kept := keep[name]; !kept {
				out = append(out, name)
			}
		}
	}
	slices.Sort(out)
	return out
}

// requestDigest identifies an agent request apart from uploaded skill IDs.
func requestDigest(b agentBody) (string, error) {
	raw, err := json.Marshal(struct {
		Params      anthropic.BetaAgentNewParams
		Passthrough map[string]any
	}{b.newParams(), b.Passthrough})
	if err != nil {
		return "", err
	}
	return digest.Bytes(raw), nil
}

// passthrough sends an agent's providers.managed-agents fields as they are.
func passthrough(fields map[string]any) []option.RequestOption {
	var opts []option.RequestOption
	for _, k := range slices.Sorted(maps.Keys(fields)) {
		opts = append(opts, option.WithJSONSet(k, fields[k]))
	}
	return opts
}

func remoteHash(a *anthropic.BetaManagedAgentsAgent) string {
	return "v" + strconv.FormatInt(a.Version, 10)
}

// baseVersion is the version an update expects to replace: the one
// refresh saw, which includes changes made outside Solaria the plan showed,
// or else the one last applied.
func baseVersion(pl *provider.Plan) int64 {
	if o := pl.Observed; o != nil && o.Exists {
		if v, err := strconv.ParseInt(strings.TrimPrefix(o.RemoteHash, "v"), 10, 64); err == nil {
			return v
		}
	}
	return priorVersion(pl.Prior)
}

func priorVersion(e *state.Entry) int64 {
	v, _ := strconv.ParseInt(e.Binding.Version, 10, 64)
	return v
}

func isStatus(err error, codes ...int) bool {
	var apiErr *anthropic.Error
	return errors.As(err, &apiErr) && slices.Contains(codes, apiErr.StatusCode)
}

// isArchived tells an "already archived" refusal from other errors.
func isArchived(err error) bool {
	return isStatus(err, http.StatusBadRequest, http.StatusConflict) && strings.Contains(strings.ToLower(err.Error()), "archived")
}

func (p *Provider) logf(format string, args ...any) {
	_, _ = fmt.Fprintf(p.log, format, args...)
}

// settings are the environment's providers.managed-agents.
type settings struct {
	namePrefix string
	keySecret  string
	platform   string
}

var (
	namePrefixPattern = regexp.MustCompile(`^[A-Za-z0-9][-_.A-Za-z0-9 ]*$`)
	secretPattern     = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)
	platforms         = []string{"linux/amd64", "linux/arm64"}
)

// readSettings reads and checks an environment's providers.managed-agents.
func readSettings(env *v1alpha1.EnvironmentSpec, placement string) (settings, diag.List) {
	s := settings{keySecret: "ANTHROPIC_ENVIRONMENT_KEY", platform: platforms[0]}
	var diags diag.List
	errorf := func(format string, args ...any) {
		diags = append(diags, diag.Errorf("", diag.Pos{}, format, args...))
	}
	for _, k := range slices.Sorted(maps.Keys(env.Providers[Name])) {
		v := env.Providers[Name][k]
		str, _ := v.(string)
		switch k {
		case "namePrefix":
			if !namePrefixPattern.MatchString(str) {
				errorf("providers.%s.namePrefix must start with a letter or digit and hold letters, digits, spaces and -_. only, not %v", Name, v)
				continue
			}
			s.namePrefix = str
		case "environmentKeySecret":
			if !secretPattern.MatchString(str) {
				errorf("providers.%s.environmentKeySecret must be a secret name in UPPER_SNAKE_CASE, not %v", Name, v)
				continue
			}
			s.keySecret = str
		case "platform":
			if !slices.Contains(platforms, str) {
				errorf("providers.%s.platform must be %s, not %v", Name, strings.Join(platforms, " or "), v)
				continue
			}
			s.platform = str
		default:
			diags = append(diags, diag.Warnf("", diag.Pos{}, "providers.%s.%s isn't a setting and is ignored; the settings are namePrefix, environmentKeySecret and platform", Name, k))
		}
	}
	if placement != PlacementSelfHosted {
		for _, k := range []string{"environmentKeySecret", "platform"} {
			if _, ok := env.Providers[Name][k]; ok {
				diags = append(diags, diag.Warnf("", diag.Pos{}, "providers.%s.%s only applies to the self-hosted placement and is ignored", Name, k))
			}
		}
	}
	return s, diags
}
