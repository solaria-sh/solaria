package gitagent

import (
	"fmt"
	"maps"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/solaria-sh/solaria/internal/api/v1alpha1"
	"github.com/solaria-sh/solaria/internal/diag"
	"github.com/solaria-sh/solaria/internal/digest"
	"github.com/solaria-sh/solaria/internal/provider"
	"github.com/solaria-sh/solaria/internal/providers/container"
	"github.com/solaria-sh/solaria/internal/resolve"
	"github.com/solaria-sh/solaria/internal/state"
)

// build is everything the provider derives from one resolved agent: the
// build context, the image to tag, and the command that runs it.
type build struct {
	// Files of the build context, by slash-separated path.
	Files map[string][]byte
	// Repository is the image name without a tag; Image adds the tag.
	Repository string
	Image      string
	Platform   string
	// Commit is the gitagent commit the image builds.
	Commit string
	Labels map[string]string
	// Base is set when sandbox.image is a Dockerfile: it is built first and
	// tagged BaseImage, and the generated Dockerfile starts from it.
	Base      *container.BuildRequest
	BaseImage string
	// Run is the docker run command, ready for a shell. The task goes
	// after it, as -p "<task>". Invocation is the same command as argv,
	// with what it needs from the caller.
	Run        string
	Invocation *state.Invocation
	// Digests of the parts that decide whether the image must be rebuilt.
	SandboxDigest, BuildDigest, RunDigest string
	// Diags are the problems and degradations found while rendering.
	Diags diag.List
}

const (
	// Context paths.
	dockerfilePath = "Dockerfile"
	configDir      = "home/.gitagent"
)

// render turns a resolved agent into a build. It reads skill files and a
// sandbox Dockerfile from disk but calls nothing.
func render(a *resolve.Agent) (*build, error) {
	r := &renderer{a: a, b: &build{Files: map[string][]byte{}}}
	return r.b, r.render()
}

type renderer struct {
	a *resolve.Agent
	b *build
}

func (r *renderer) errorf(format string, args ...any) {
	r.b.Diags = append(r.b.Diags, diag.Errorf("", diag.Pos{}, "%s on %s/%s: %s", r.a.ID(), Name, PlacementDocker, fmt.Sprintf(format, args...)))
}

func (r *renderer) warnf(format string, args ...any) {
	r.b.Diags = append(r.b.Diags, diag.Warnf("", diag.Pos{}, "%s on %s/%s: %s", r.a.ID(), Name, PlacementDocker, fmt.Sprintf(format, args...)))
}

func (r *renderer) render() error {
	a, b := r.a, r.b
	settings := r.settings()
	b.Platform = settings.platform
	b.Commit = settings.commit
	b.Repository = "solaria/" + a.Target + "/" + a.Name
	b.Image = b.Repository + ":" + strings.TrimPrefix(a.ResolvedDigest, "sha256:")[:12]

	cfg, err := r.config()
	if err != nil {
		return err
	}
	b.Files[configDir+"/gitagent.yaml"] = cfg
	if len(a.Definition.Instructions) > 0 {
		parts := make([]string, len(a.Definition.Instructions))
		for i, s := range a.Definition.Instructions {
			parts[i] = strings.TrimRight(s, "\n")
		}
		b.Files[configDir+"/prompts/rules.md"] = []byte(strings.Join(parts, "\n\n") + "\n")
	}
	for _, name := range slices.Sorted(maps.Keys(a.Definition.Skills)) {
		dir := a.SkillDirs[name]
		for _, rel := range slices.Sorted(maps.Keys(a.Definition.Skills[name].Files)) {
			content, err := resolve.ReadText(filepath.Join(dir, filepath.FromSlash(rel)))
			if err != nil {
				return fmt.Errorf("read skill %s: %w", name, err)
			}
			b.Files[path.Join(configDir, "skills", name, rel)] = content
		}
	}

	base, err := r.base()
	if err != nil {
		return err
	}
	allow, deny := r.tools()
	b.Files[dockerfilePath] = dockerfile(dockerfileData{
		LogicalID: a.ID(),
		Target:    a.Target,
		Commit:    b.Commit,
		Base:      base,
		Packages:  container.Packages(a.Sandbox),
		Allow:     allow,
		Deny:      deny,
	})

	b.Invocation = r.invocation()
	b.Run = container.ShellCommand(b.Invocation.Argv)
	b.Labels = map[string]string{
		labelPrefix + "logical-id":      a.ID(),
		labelPrefix + "target":          a.Target,
		labelPrefix + "spec-digest":     a.SpecDigest,
		labelPrefix + "resolved-digest": a.ResolvedDigest,
		labelPrefix + "gitagent-commit": b.Commit,
	}
	if d := a.Definition.Description; d != "" {
		b.Labels["org.opencontainers.image.description"] = d
	}

	if b.SandboxDigest, err = digest.Of(map[string]any{"sandbox": a.Sandbox, "platform": b.Platform, "base": base}); err != nil {
		return err
	}
	files := map[string]string{}
	for p, c := range b.Files {
		files[p] = digest.Bytes(c)
	}
	if b.BuildDigest, err = digest.Of(files); err != nil {
		return err
	}
	b.RunDigest = digest.Bytes([]byte(b.Run))
	r.sandboxWarnings()
	return nil
}

// labelPrefix namespaces Solaria's image labels.
const labelPrefix = "sh.solaria."

type settings struct {
	platform, commit string
}

// settings reads the target's providers.gitagent and checks the
// agent's own.
func (r *renderer) settings() settings {
	s := settings{platform: DefaultPlatform, commit: GitagentCommit}
	set := r.a.Env.Providers[Name]
	for _, k := range slices.Sorted(maps.Keys(set)) {
		v := set[k]
		switch k {
		case "platform":
			p, _ := v.(string)
			if !slices.Contains(platforms, p) {
				r.errorf("Target providers.gitagent.platform must be %s, not %v", strings.Join(platforms, " or "), v)
				continue
			}
			s.platform = p
		case "commit":
			c, _ := v.(string)
			if !commitPattern.MatchString(c) {
				r.errorf("Target providers.gitagent.commit must be a full 40-character commit hash of gitagent, such as %s, not %v", GitagentCommit, v)
				continue
			}
			s.commit = c
		default:
			r.warnf("Target providers.gitagent.%s isn't a gitagent setting and is ignored; the settings are: platform, commit", k)
		}
	}
	for _, k := range slices.Sorted(maps.Keys(r.a.Definition.Providers[Name])) {
		r.warnf("providers.gitagent.%s isn't a gitagent setting and is ignored; gitagent has no per-agent settings", k)
	}
	return s
}

// platforms the provider builds for.
var platforms = []string{"linux/amd64", "linux/arm64"}

// commitPattern is a full commit hash: a branch or a short hash could
// build different sources tomorrow.
var commitPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

// gitagentConfig is gitagent.yaml in gitagent's config layer
// (crates/loader/src/config.rs at the pinned commit).
type gitagentConfig struct {
	Model          string               `yaml:"model"`
	FallbackModels []string             `yaml:"fallback_models"`
	MaxTurns       int                  `yaml:"max_turns,omitempty"`
	TimeoutS       int                  `yaml:"timeout_s,omitempty"`
	Skills         bool                 `yaml:"skills"`
	Memory         bool                 `yaml:"memory"`
	Learning       string               `yaml:"learning"`
	MCPServers     map[string]mcpServer `yaml:"mcp_servers"`
}

// mcpServer is one entry of mcp_servers (crates/loader/src/mcp.rs).
type mcpServer struct {
	Command string            `yaml:"command,omitempty"`
	Args    []string          `yaml:"args,omitempty"`
	Env     map[string]string `yaml:"env,omitempty"`
	Type    string            `yaml:"type,omitempty"`
	URL     string            `yaml:"url,omitempty"`
	Headers map[string]string `yaml:"headers,omitempty"`
}

func (r *renderer) config() ([]byte, error) {
	d := r.a.Definition
	cfg := gitagentConfig{
		Model:          d.Model.Preferred,
		FallbackModels: d.Model.Fallback,
		MaxTurns:       d.Limits.MaxTurns,
		Skills:         len(d.Skills) > 0,
		Learning:       "off",
		MCPServers:     map[string]mcpServer{},
	}
	if d.Model.Effort != "" {
		r.warnf("model.effort is ignored; gitagent sets reasoning through its model profile")
	}
	if d.Limits.Timeout != "" {
		t, err := time.ParseDuration(d.Limits.Timeout)
		if err != nil {
			return nil, fmt.Errorf("limits.timeout: %w", err)
		}
		cfg.TimeoutS = int(t.Round(time.Second) / time.Second)
	}
	for _, name := range slices.Sorted(maps.Keys(d.Tools.MCP)) {
		cfg.MCPServers[name] = r.mcpServer(name, d.Tools.MCP[name].Spec)
	}
	out, err := yaml.Marshal(cfg)
	if err != nil {
		return nil, err
	}
	header := fmt.Sprintf("# Rendered by Solaria from %s (%s).\n# Change the card and run solaria apply instead of editing this file.\n", r.a.ID(), r.a.ResolvedDigest)
	return append([]byte(header), out...), nil
}

// mcpServer picks the server's transport: a remote first, so the agent uses
// the same server it would on a cloud host, then a stdio package.
func (r *renderer) mcpServer(name string, s v1alpha1.McpServerSpec) mcpServer {
	for _, want := range []v1alpha1.RemoteType{v1alpha1.RemoteStreamableHTTP, v1alpha1.RemoteSSE} {
		for _, rem := range s.Remotes {
			if rem.Type != want {
				continue
			}
			m := mcpServer{URL: rem.URL, Headers: r.values(rem.Headers)}
			if rem.Type == v1alpha1.RemoteSSE {
				m.Type = "sse"
			}
			return m
		}
	}
	// Capabilities guarantee a package when there is no remote.
	pkg := s.Packages[0]
	m := mcpServer{Env: r.values(pkg.EnvironmentVariables)}
	switch pkg.RegistryType {
	case v1alpha1.RegistryNPM:
		m.Command, m.Args = "npx", []string{"-y", pkg.Identifier + "@" + pkg.Version}
	case v1alpha1.RegistryPyPI:
		m.Command, m.Args = "uvx", []string{pkg.Identifier + "==" + pkg.Version}
	default:
		r.errorf("McpServer %q: gitagent can't run %s packages", name, pkg.RegistryType)
	}
	for _, arg := range pkg.PackageArguments {
		if arg.Type == v1alpha1.ArgumentNamed {
			m.Args = append(m.Args, arg.Name)
		}
		m.Args = append(m.Args, arg.Value)
	}
	return m
}

// values turns headers or variables into gitagent's form: a secret becomes
// ${NAME}, which gitagent fills from the container's environment when it
// connects. The value never enters the image.
func (r *renderer) values(kvs []v1alpha1.KeyValue) map[string]string {
	if len(kvs) == 0 {
		return nil
	}
	out := map[string]string{}
	for _, kv := range kvs {
		if kv.SecretRef != "" {
			out[kv.Name] = "${" + kv.SecretRef + "}"
		} else {
			out[kv.Name] = kv.Value
		}
	}
	return out
}

// gitagent's built-in tools at the pinned commit (crates/tools/src).
var (
	builtinTools = map[string][]string{
		"shell": {"exec", "write_stdin"},
		"read":  {"read_file", "image_read"},
		"write": {"write_file"},
		"edit":  {"str_replace", "apply_patch"},
	}
	// alwaysOn are offered whatever the card says: they act on nothing
	// outside the run.
	alwaysOn = []string{"skill", "todo"}
	// gated are only offered when the card asks for them.
	gated = []string{"apply_patch", "exec", "image_read", "memory", "plan", "read_file", "str_replace", "subagent", "workflow", "write_file", "write_stdin"}
)

// tools decides --allowed-tools or --disallowed-tools. gitagent filters by
// exact name, so an allow list can only be used when the card names every
// MCP tool; otherwise the built-ins the card leaves out are denied instead.
// Tools under permissions.ask are left out of the allow list, or denied.
func (r *renderer) tools() (allow, deny []string) {
	d := r.a.Definition
	var enabled []string
	for _, t := range d.Tools.Builtin {
		names, ok := builtinTools[t]
		if ok {
			enabled = append(enabled, names...)
			continue
		}
		switch t {
		case "glob", "grep":
			if slices.Contains(d.Tools.Builtin, "shell") {
				r.warnf("%s has no gitagent tool of its own; the agent can do it through shell", t)
			} else {
				r.warnf("%s has no gitagent tool, and without shell this agent can't search files", t)
			}
		default:
			r.warnf("%s isn't available on gitagent; the agent runs without it", t)
		}
	}

	var asked []string
	for _, t := range d.Permissions.Ask {
		if server, tool, ok := strings.Cut(t, "/"); ok {
			asked = append(asked, server+"__"+tool)
		} else {
			asked = append(asked, builtinTools[t]...)
		}
	}

	var narrowed, open []string
	for _, name := range slices.Sorted(maps.Keys(d.Tools.MCP)) {
		if d.Tools.MCP[name].AllowTools != nil {
			narrowed = append(narrowed, name)
		} else {
			open = append(open, name)
		}
	}
	switch {
	case len(open) == 0:
		allow = slices.Concat(enabled, alwaysOn)
		for _, name := range narrowed {
			for _, t := range d.Tools.MCP[name].AllowTools {
				allow = append(allow, name+"__"+t)
			}
		}
		allow = slices.DeleteFunc(allow, func(t string) bool { return slices.Contains(asked, t) })
	case len(narrowed) == 0:
		for _, t := range gated {
			if !slices.Contains(enabled, t) {
				deny = append(deny, t)
			}
		}
		deny = append(deny, asked...)
	default:
		r.errorf("gitagent can narrow MCP tools only when every McpServer ref lists allowTools: %s list them and %s don't, and gitagent can't know their tool names before connecting",
			strings.Join(narrowed, ", "), strings.Join(open, ", "))
	}
	slices.Sort(allow)
	slices.Sort(deny)
	return slices.Compact(allow), slices.Compact(deny)
}

// base returns the FROM line's image, building a sandbox Dockerfile first
// when the target names one.
func (r *renderer) base() (string, error) {
	image, build, err := container.SandboxBase(r.a.Sandbox, r.a.SandboxDir,
		"solaria/"+r.a.Target+"/"+r.a.Name+"-sandbox", r.b.Platform,
		map[string]string{labelPrefix + "target": r.a.Target})
	if build != nil {
		r.b.Base, r.b.BaseImage = build, image
	}
	return image, err
}

func (r *renderer) sandboxWarnings() {
	sb := r.a.Sandbox
	if sb == nil || sb.Network == nil || sb.Network.Mode != v1alpha1.NetworkUnrestricted {
		r.warnf("the sandbox's network is limited, but docker doesn't enforce host lists yet: the container can reach any host")
	}
	if sb != nil && sb.Resources != nil && sb.Resources.Disk != "" {
		r.warnf("its Environment's spec.resources.disk isn't enforced on docker")
	}
	for _, pkg := range container.OptionLikePackages(sb) {
		r.errorf("sandbox package %q starts with '-'; package managers would read it as an option", pkg)
	}
}

// invocation builds the docker run command. It names secrets and API keys but
// never contains their values: docker copies them from the caller's
// environment, or from the target's dotenv file.
func (r *renderer) invocation() *state.Invocation {
	a := r.a
	inv := &state.Invocation{Task: state.TaskInput{Flag: "-p"}}
	provider.SetWorkspace(inv, a, state.WorkspaceMount)
	parts := []string{"docker", "run", "--rm", "--platform", r.b.Platform, "--memory", container.Memory(a.Sandbox)}
	parts = append(parts, "--volume", container.WorkspaceVolume(a))

	secrets := a.SecretRefs()
	vars := a.SecretVars()
	for _, v := range slices.Sorted(maps.Keys(vars)) {
		if secret := vars[v]; v != secret {
			r.errorf("a Vault credential sets %s from secret %s, but gitagent passes secrets to the container under their own names; name the variable after the secret", v, secret)
		}
	}
	if a.Env.Secrets != nil && a.Env.Secrets.Source == v1alpha1.SecretsDotenv {
		if len(a.Definition.Vaults) > 0 {
			r.warnf("docker passes the whole dotenv file to the container, so the agent's vaults don't limit which secrets it sees; use secrets.source env to scope them")
		}
		p := a.Env.Secrets.Path
		if p == "" {
			p = ".env"
		}
		inv.EnvFile = filepath.Join(a.EnvDir, filepath.FromSlash(p))
		parts = append(parts, "--env-file", inv.EnvFile)
	} else {
		for _, s := range secrets {
			parts = append(parts, "--env", s)
			inv.Env = append(inv.Env, s)
		}
	}
	for _, k := range modelKeys(a.Definition.Model) {
		parts = append(parts, "--env", k)
		inv.Env = append(inv.Env, k)
	}
	id := provider.IdentityEnv(a)
	for _, k := range slices.Sorted(maps.Keys(id)) {
		parts = append(parts, "--env", k+"="+id[k])
	}

	if t := a.Env.Telemetry; t != nil {
		env := []string{"OTEL_EXPORTER_OTLP_ENDPOINT=" + t.Endpoint, "OTEL_SERVICE_NAME=" + a.Name}
		if t.Protocol != "" {
			env = append(env, "OTEL_EXPORTER_OTLP_PROTOCOL="+string(t.Protocol))
		}
		var headers []string
		for _, h := range t.Headers {
			if h.SecretRef != "" {
				r.warnf("telemetry header %s comes from a secret, which can't be passed to gitagent's exporter yet; it's left out", h.Name)
				continue
			}
			headers = append(headers, h.Name+"="+h.Value)
		}
		if len(headers) > 0 {
			env = append(env, "OTEL_EXPORTER_OTLP_HEADERS="+strings.Join(headers, ","))
		}
		// gitagent doesn't read these yet (see the gitagent spike); they
		// are set so traces carry Solaria's identity once it does.
		env = append(env, "OTEL_RESOURCE_ATTRIBUTES="+provider.ResourceAttributes(a, r.b.Repository))
		for _, e := range env {
			parts = append(parts, "--env", e)
		}
		if localhost.MatchString(t.Endpoint) {
			r.warnf("telemetry.endpoint %s points at the container itself; use host.docker.internal to reach the host", t.Endpoint)
		}
	}
	parts = append(parts, r.b.Image)
	inv.Argv = parts
	return inv
}

var localhost = regexp.MustCompile(`^[a-z]+://(localhost|127\.0\.0\.1|\[::1\])(:|/|$)`)

// modelKeys lists the variables gitagent reads API keys from, for every
// model the agent may use (crates/provider/src at the pinned commit).
func modelKeys(m resolve.Model) []string {
	var out []string
	for _, ref := range append([]string{m.Preferred}, m.Fallback...) {
		p, _, _ := strings.Cut(ref, ":")
		switch p {
		case "anthropic":
			out = append(out, "ANTHROPIC_API_KEY")
		case "openai":
			out = append(out, "OPENAI_API_KEY")
		case "google", "gemini":
			out = append(out, "GEMINI_API_KEY")
		case "openrouter":
			out = append(out, "OPENROUTER_API_KEY")
		case "ollama", "vllm", "scripted":
		default:
			upper := strings.ToUpper(strings.ReplaceAll(p, "-", "_"))
			out = append(out, upper+"_API_KEY", upper+"_BASE_URL")
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// componentChanges compares a prior binding with a new build: the gitagent
// commit, then the digests. A binding from before Solaria recorded the
// commit was built from the old default, and its image digest shows a
// change of commit anyway.
func componentChanges(outputs map[string]string, b *build) []provider.Change {
	var out []provider.Change
	if old := outputs[outCommit]; old != "" && old != b.Commit {
		out = append(out, provider.Change{Path: "gitagent", Old: old, New: b.Commit})
	}
	for _, c := range []struct{ path, key, now string }{
		{"sandbox", outSandboxDigest, b.SandboxDigest},
		{"image", outBuildDigest, b.BuildDigest},
		{"run command", outRunDigest, b.RunDigest},
	} {
		if old := outputs[c.key]; old != c.now {
			if old == "" {
				old = "none"
			}
			out = append(out, provider.Change{Path: c.path, Old: provider.ShortDigest(old), New: provider.ShortDigest(c.now)})
		}
	}
	return out
}
