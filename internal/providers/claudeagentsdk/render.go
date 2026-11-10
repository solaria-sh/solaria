package claudeagentsdk

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/solaria-sh/solaria/internal/api/v1alpha1"
	"github.com/solaria-sh/solaria/internal/diag"
	"github.com/solaria-sh/solaria/internal/digest"
	"github.com/solaria-sh/solaria/internal/providers/process"
	"github.com/solaria-sh/solaria/internal/resolve"
)

// bundle is everything the provider derives from one resolved agent: the
// files of its bundle and what identifies them.
type bundle struct {
	// Files of the bundle, by slash-separated path. The SDK itself isn't
	// among them; apply installs it next to them.
	Files map[string][]byte
	// Runtime is node or python; SDKVersion is the package version
	// installed for it.
	Runtime, SDKVersion string
	// BuildDigest covers every file, so the SDK version, the runtime and
	// Solaria's launcher are in it as well as the definition. Its first 12
	// hex digits name the bundle's directory.
	BuildDigest string
	// Secrets are the names of the secrets a run is given.
	Secrets []string
	// Dotenv is the absolute path of the dotenv file the secrets come
	// from, when the environment has one.
	Dotenv string
	// Docker is set on the docker placement.
	Docker *dockerParts
	// Diags are the problems and degradations found while rendering.
	Diags diag.List
}

// ID is the bundle's directory name.
func (b *bundle) ID() string { return strings.TrimPrefix(b.BuildDigest, digest.Prefix)[:12] }

// Runtimes and the SDK package each installs, at the version Solaria was
// tested with. An Environment can pick another version with
// providers.claude-agent-sdk.version.
const (
	RuntimeNode   = "node"
	RuntimePython = "python"

	NodePackage   = "@anthropic-ai/claude-agent-sdk"
	PythonPackage = "claude-agent-sdk"

	DefaultNodeVersion   = "0.3.319"
	DefaultPythonVersion = "0.2.178"
)

// Bundle paths.
const (
	optionsFile      = "options.json"
	isolationFile    = "isolation.json"
	systemPromptFile = "system-prompt.md"
	pluginDir        = "plugin"
	// cacheDir is the package cache shared by every bundle of the
	// provider, relative to a bundle: .solaria/claude-agent-sdk/.cache. Its
	// leading dot keeps it apart from environment names.
	cacheDir = "../../../.cache"
)

// render turns a resolved agent into a bundle. It reads skill files from
// disk but runs nothing.
func render(a *resolve.Agent) (*bundle, error) {
	r := &renderer{a: a, b: &bundle{Files: map[string][]byte{}}}
	return r.b, r.render()
}

type renderer struct {
	a *resolve.Agent
	b *bundle
	// platform is the docker placement's image platform.
	platform string
	// binaries are the programs the run needs on PATH.
	binaries []string
}

func (r *renderer) docker() bool { return r.a.Placement == PlacementDocker }

func (r *renderer) where() string {
	return fmt.Sprintf("%s on %s/%s", r.a.ID(), Name, r.a.Placement)
}

func (r *renderer) errorf(format string, args ...any) {
	r.b.Diags = append(r.b.Diags, diag.Errorf("", diag.Pos{}, "%s: %s", r.where(), fmt.Sprintf(format, args...)))
}

func (r *renderer) warnf(format string, args ...any) {
	r.b.Diags = append(r.b.Diags, diag.Warnf("", diag.Pos{}, "%s: %s", r.where(), fmt.Sprintf(format, args...)))
}

// bundleFile is options.json: the SDK options plus what the launcher needs
// around them. The launcher fills in what only a run knows: the system
// prompt's text, absolute paths, the environment and the secrets.
type bundleFile struct {
	Agent agentInfo `json:"agent"`
	SDK   sdkInfo   `json:"sdk"`
	// CacheDir is relative to the bundle.
	CacheDir string `json:"cacheDir"`
	// Timeout is limits.timeout as written; TimeoutMS is what the launcher
	// enforces. Zero means no limit.
	Timeout   string `json:"timeout,omitempty"`
	TimeoutMS int64  `json:"timeoutMs,omitempty"`
	// SystemPromptFile is relative to the bundle.
	SystemPromptFile string     `json:"systemPromptFile,omitempty"`
	Options          sdkOptions `json:"options"`
}

type agentInfo struct {
	LogicalID      string `json:"logicalId"`
	Environment    string `json:"environment"`
	ResolvedDigest string `json:"resolvedDigest"`
}

type sdkInfo struct {
	Runtime string `json:"runtime"`
	Package string `json:"package"`
	Version string `json:"version"`
}

// sdkOptions are the SDK's options, with the TypeScript SDK's names; the
// Python launcher renames them.
type sdkOptions struct {
	Model           string               `json:"model"`
	FallbackModel   string               `json:"fallbackModel,omitempty"`
	Effort          string               `json:"effort,omitempty"`
	MaxTurns        int                  `json:"maxTurns,omitempty"`
	Tools           []string             `json:"tools"`
	AllowedTools    []string             `json:"allowedTools"`
	DisallowedTools []string             `json:"disallowedTools"`
	PermissionMode  string               `json:"permissionMode"`
	SettingSources  []string             `json:"settingSources"`
	StrictMCPConfig bool                 `json:"strictMcpConfig"`
	MCPServers      map[string]mcpServer `json:"mcpServers"`
	Skills          []string             `json:"skills,omitempty"`
	// Plugins' paths are relative to the bundle.
	Plugins []plugin `json:"plugins,omitempty"`
}

// mcpServer is one entry of mcpServers. A value from a secret is written
// as {"secretRef": NAME}, and the launcher puts the secret in its place.
type mcpServer struct {
	Type    string         `json:"type"`
	Command string         `json:"command,omitempty"`
	Args    []string       `json:"args,omitempty"`
	Env     map[string]any `json:"env,omitempty"`
	URL     string         `json:"url,omitempty"`
	Headers map[string]any `json:"headers,omitempty"`
}

type plugin struct {
	Type string `json:"type"`
	Path string `json:"path"`
}

type secretRef struct {
	SecretRef string `json:"secretRef"`
}

func (r *renderer) render() error {
	a, b := r.a, r.b
	r.settings()

	opts := r.options()
	bf := bundleFile{
		Agent:    agentInfo{LogicalID: a.ID(), Environment: a.Environment, ResolvedDigest: a.ResolvedDigest},
		SDK:      sdkInfo{Runtime: b.Runtime, Package: sdkPackage(b.Runtime), Version: b.SDKVersion},
		CacheDir: cacheDir,
		Options:  opts,
	}
	if r.docker() {
		bf.CacheDir = imageCache
	}
	if t := a.Definition.Limits.Timeout; t != "" {
		d, err := time.ParseDuration(t)
		if err != nil {
			return fmt.Errorf("limits.timeout: %w", err)
		}
		bf.Timeout, bf.TimeoutMS = t, d.Milliseconds()
	}
	if len(a.Definition.Instructions) > 0 {
		parts := make([]string, len(a.Definition.Instructions))
		for i, s := range a.Definition.Instructions {
			parts[i] = strings.TrimRight(s, "\n")
		}
		b.Files[systemPromptFile] = []byte(strings.Join(parts, "\n\n") + "\n")
		bf.SystemPromptFile = systemPromptFile
	}
	if err := r.plugin(); err != nil {
		return err
	}
	b.Files[optionsFile] = marshal(bf)

	needs := process.Needs{
		Binaries: binaries(opts),
		Pass:     []string{"ANTHROPIC_API_KEY"},
		Set:      r.env(),
		Scratch:  map[string]string{"CLAUDE_CONFIG_DIR": "home/.claude"},
	}
	if r.docker() {
		needs.Path = imagePath
	}
	iso := process.For(a, needs)
	r.binaries = iso.Binaries
	b.Dotenv = iso.Dotenv
	if r.docker() {
		// docker run puts the secrets in the container's environment, from
		// the dotenv file or the caller's; the host's path means nothing
		// inside.
		iso.Dotenv = ""
	}
	b.Secrets = iso.Secrets
	b.Files[isolationFile] = iso.JSON()

	switch b.Runtime {
	case RuntimeNode:
		b.Files["isolate.mjs"] = process.PreludeJS
		b.Files["launch.mjs"] = launchJS
		b.Files["package.json"] = packageJSON(a, b.SDKVersion)
	case RuntimePython:
		b.Files["isolate.py"] = process.PreludePython
		b.Files["launch.py"] = launchPython
		b.Files["requirements.txt"] = []byte(PythonPackage + "==" + b.SDKVersion + "\n")
	}

	if r.docker() {
		if err := r.renderDocker(); err != nil {
			return err
		}
	}

	files := map[string]string{}
	for p, c := range b.Files {
		files[p] = digest.Bytes(c)
	}
	var err error
	if r.docker() {
		// The platform isn't in any file, but it's a different image.
		b.BuildDigest, err = digest.Of(map[string]any{"files": files, "platform": r.platform})
	} else {
		b.BuildDigest, err = digest.Of(files)
	}
	if err != nil {
		return err
	}
	if d := b.Docker; d != nil {
		d.Image = d.Repository + ":" + b.ID()
		d.Argv = r.dockerArgv()
	}
	return nil
}

func sdkPackage(runtime string) string {
	if runtime == RuntimePython {
		return PythonPackage
	}
	return NodePackage
}

// settings reads the environment's providers.claude-agent-sdk and checks
// the agent's own.
func (r *renderer) settings() {
	b := r.b
	b.Runtime = RuntimeNode
	r.platform = DefaultPlatform
	var version string
	set := r.a.Env.Providers[Name]
	for _, k := range slices.Sorted(maps.Keys(set)) {
		v := set[k]
		switch k {
		case "runtime":
			s, _ := v.(string)
			if s != RuntimeNode && s != RuntimePython {
				r.errorf("Environment providers.%s.runtime must be node or python, not %v", Name, v)
				continue
			}
			b.Runtime = s
		case "version":
			s, _ := v.(string)
			if !versionPattern.MatchString(s) {
				r.errorf("Environment providers.%s.version must be an exact package version, such as %s, not %v", Name, DefaultNodeVersion, v)
				continue
			}
			version = s
		case "platform":
			s, _ := v.(string)
			switch {
			case !r.docker():
				r.warnf("Environment providers.%s.platform only applies to the docker placement and is ignored", Name)
			case !slices.Contains(platforms, s):
				r.errorf("Environment providers.%s.platform must be %s, not %v", Name, strings.Join(platforms, " or "), v)
			default:
				r.platform = s
			}
		default:
			r.warnf("Environment providers.%s.%s isn't a setting of this provider and is ignored; the settings are: runtime, version, platform", Name, k)
		}
	}
	for _, k := range slices.Sorted(maps.Keys(r.a.Definition.Providers[Name])) {
		r.warnf("providers.%s.%s isn't a setting of this provider and is ignored; it has no per-agent settings", Name, k)
	}
	switch {
	case version != "":
		b.SDKVersion = version
	case b.Runtime == RuntimePython:
		b.SDKVersion = DefaultPythonVersion
	default:
		b.SDKVersion = DefaultNodeVersion
	}
}

// versionPattern is an exact version, as npm and pip both write it.
var versionPattern = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+([-.+]?[0-9A-Za-z][0-9A-Za-z.+-]*)?$`)

// builtinTools maps Solaria's built-in tools to Claude Code's.
var builtinTools = map[string]string{
	"shell":      "Bash",
	"read":       "Read",
	"write":      "Write",
	"edit":       "Edit",
	"glob":       "Glob",
	"grep":       "Grep",
	"web_fetch":  "WebFetch",
	"web_search": "WebSearch",
}

// options maps the definition to the SDK's options.
//
// tools is the exact list of built-in tools the agent sees. allowedTools
// approves them and the MCP tools in advance, and permissionMode dontAsk
// denies every other call instead of asking, since nobody can answer in an
// unattended run. Tools under permissions.ask are left out of tools and
// allowedTools, and denied by name.
func (r *renderer) options() sdkOptions {
	d := r.a.Definition
	o := sdkOptions{
		Model:           anthropicModel(d.Model.Preferred),
		Effort:          string(d.Model.Effort),
		MaxTurns:        d.Limits.MaxTurns,
		Tools:           []string{},
		AllowedTools:    []string{},
		DisallowedTools: []string{},
		PermissionMode:  "dontAsk",
		SettingSources:  []string{},
		StrictMCPConfig: true,
		MCPServers:      map[string]mcpServer{},
	}

	var fallback []string
	for _, m := range d.Model.Fallback {
		if p, _, _ := strings.Cut(m, ":"); p == "anthropic" {
			fallback = append(fallback, anthropicModel(m))
		}
	}
	if len(fallback) > 2 {
		r.warnf("Claude Code caps a fallback chain at three models, so fallback models after %s may be ignored", fallback[1])
	}
	o.FallbackModel = strings.Join(fallback, ",")

	asked := map[string]bool{}
	for _, t := range d.Permissions.Ask {
		if server, tool, ok := strings.Cut(t, "/"); ok {
			asked[mcpTool(server, tool)] = true
			o.DisallowedTools = append(o.DisallowedTools, mcpTool(server, tool))
		} else if name, ok := builtinTools[t]; ok {
			asked[name] = true
			o.DisallowedTools = append(o.DisallowedTools, name)
		}
	}
	for _, t := range d.Tools.Builtin {
		if name, ok := builtinTools[t]; ok && !asked[name] {
			o.Tools = append(o.Tools, name)
		}
	}
	if len(d.Skills) > 0 {
		o.Tools = append(o.Tools, "Skill")
		for _, name := range slices.Sorted(maps.Keys(d.Skills)) {
			o.Skills = append(o.Skills, r.a.Name+":"+name)
		}
		o.Plugins = []plugin{{Type: "local", Path: pluginDir}}
	}
	o.AllowedTools = slices.Clone(o.Tools)

	var narrowed []string
	for _, name := range slices.Sorted(maps.Keys(d.Tools.MCP)) {
		ref := d.Tools.MCP[name]
		o.MCPServers[name] = r.mcpServer(name, ref.Spec)
		if ref.AllowTools == nil {
			o.AllowedTools = append(o.AllowedTools, mcpTool(name, "*"))
			continue
		}
		narrowed = append(narrowed, name)
		for _, t := range ref.AllowTools {
			if !asked[mcpTool(name, t)] {
				o.AllowedTools = append(o.AllowedTools, mcpTool(name, t))
			}
		}
	}
	if len(narrowed) > 0 {
		r.warnf("allowTools on %s: the other tools of these servers are denied when called, but the agent still sees them", strings.Join(narrowed, ", "))
	}
	slices.Sort(o.AllowedTools)
	slices.Sort(o.DisallowedTools)
	return o
}

// anthropicModel drops the model provider: the SDK takes Anthropic model
// names only.
func anthropicModel(ref string) string {
	_, model, ok := strings.Cut(ref, ":")
	if !ok {
		return ref
	}
	return model
}

// mcpTool is an MCP tool's name inside Claude Code.
func mcpTool(server, tool string) string { return "mcp__" + server + "__" + tool }

// mcpServer picks the server's transport: a remote first, so the agent uses
// the same server it would on a cloud host, then a stdio package.
func (r *renderer) mcpServer(name string, s v1alpha1.McpServerSpec) mcpServer {
	for _, want := range []v1alpha1.RemoteType{v1alpha1.RemoteStreamableHTTP, v1alpha1.RemoteSSE} {
		for _, rem := range s.Remotes {
			if rem.Type != want {
				continue
			}
			m := mcpServer{Type: "http", URL: rem.URL, Headers: values(rem.Headers)}
			if rem.Type == v1alpha1.RemoteSSE {
				m.Type = "sse"
			}
			return m
		}
	}
	// Capabilities guarantee a package when there is no remote.
	pkg := s.Packages[0]
	m := mcpServer{Type: "stdio", Env: values(pkg.EnvironmentVariables)}
	switch pkg.RegistryType {
	case v1alpha1.RegistryNPM:
		m.Command, m.Args = "npx", []string{"-y", pkg.Identifier + "@" + pkg.Version}
	case v1alpha1.RegistryPyPI:
		m.Command, m.Args = "uvx", []string{pkg.Identifier + "==" + pkg.Version}
	default:
		r.errorf("McpServer %q: the process placement can't run %s packages", name, pkg.RegistryType)
	}
	for _, arg := range pkg.PackageArguments {
		if arg.Type == v1alpha1.ArgumentNamed {
			m.Args = append(m.Args, arg.Name)
		}
		m.Args = append(m.Args, arg.Value)
	}
	return m
}

// values turns headers or variables into the SDK's form. A secret is
// written as a reference, never its value.
func values(kvs []v1alpha1.KeyValue) map[string]any {
	if len(kvs) == 0 {
		return nil
	}
	out := map[string]any{}
	for _, kv := range kvs {
		if kv.SecretRef != "" {
			out[kv.Name] = secretRef{SecretRef: kv.SecretRef}
		} else {
			out[kv.Name] = kv.Value
		}
	}
	return out
}

// binaries are the programs a run needs on PATH besides the runtime: the
// package runners of the stdio MCP servers it uses.
func binaries(o sdkOptions) []string {
	var out []string
	for _, s := range o.MCPServers {
		if s.Command != "" {
			out = append(out, s.Command)
		}
	}
	return out
}

// env is what every run sets: Claude Code's switches that keep it from
// reading memory and instruction files or reaching services the card
// doesn't name, and the environment's telemetry.
func (r *renderer) env() map[string]string {
	a := r.a
	env := map[string]string{
		"CLAUDE_CODE_DISABLE_AUTO_MEMORY":          "1",
		"CLAUDE_CODE_DISABLE_CLAUDE_MDS":           "1",
		"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1",
		"ENABLE_CLAUDEAI_MCP_SERVERS":              "false",
		"CLAUDE_AGENT_SDK_CLIENT_APP":              "solaria",
	}
	t := a.Env.Telemetry
	if t == nil {
		return env
	}
	env["CLAUDE_CODE_ENABLE_TELEMETRY"] = "1"
	env["OTEL_METRICS_EXPORTER"] = "otlp"
	env["OTEL_LOGS_EXPORTER"] = "otlp"
	env["OTEL_EXPORTER_OTLP_ENDPOINT"] = t.Endpoint
	env["OTEL_SERVICE_NAME"] = a.Name
	if t.Protocol != "" {
		env["OTEL_EXPORTER_OTLP_PROTOCOL"] = string(t.Protocol)
	}
	var headers []string
	for _, h := range t.Headers {
		if h.SecretRef != "" {
			r.warnf("telemetry header %s comes from a secret, which can't be passed to Claude Code's exporter yet; it's left out", h.Name)
			continue
		}
		headers = append(headers, h.Name+"="+h.Value)
	}
	if len(headers) > 0 {
		env["OTEL_EXPORTER_OTLP_HEADERS"] = strings.Join(headers, ",")
	}
	env["OTEL_RESOURCE_ATTRIBUTES"] = strings.Join([]string{
		"gen_ai.agent.name=" + a.Name,
		"gen_ai.agent.version=" + a.ResolvedDigest,
		"solaria.environment=" + a.Environment,
	}, ",")
	return env
}

// plugin writes the skills as a local plugin named after the agent, so
// they load with every setting source off. Inside a run each skill is
// <agent>:<skill>.
func (r *renderer) plugin() error {
	a := r.a
	if len(a.Definition.Skills) == 0 {
		return nil
	}
	manifest := map[string]string{"name": a.Name}
	if a.Definition.Description != "" {
		manifest["description"] = a.Definition.Description
	}
	r.b.Files[pluginDir+"/.claude-plugin/plugin.json"] = marshal(manifest)
	for _, name := range slices.Sorted(maps.Keys(a.Definition.Skills)) {
		dir := a.SkillDirs[name]
		for _, rel := range slices.Sorted(maps.Keys(a.Definition.Skills[name].Files)) {
			content, err := resolve.ReadText(filepath.Join(dir, filepath.FromSlash(rel)))
			if err != nil {
				return fmt.Errorf("read skill %s: %w", name, err)
			}
			r.b.Files[path.Join(pluginDir, "skills", name, rel)] = content
		}
	}
	return nil
}

// packageJSON pins the SDK for npm. npm writes package-lock.json next to
// it when apply installs.
func packageJSON(a *resolve.Agent, version string) []byte {
	return marshal(map[string]any{
		"name":         "solaria-" + a.Environment + "-" + a.Name,
		"private":      true,
		"type":         "module",
		"description":  "Rendered by Solaria from " + a.ID() + " (" + a.ResolvedDigest + "). Run solaria apply instead of editing this bundle.",
		"dependencies": map[string]string{NodePackage: version},
	})
}

// marshal encodes JSON the way every bundle file is written: indented,
// with sorted keys and a final newline.
func marshal(v any) []byte {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		panic(fmt.Sprintf("bundle file doesn't encode: %v", err)) // a bug, not input
	}
	return b.Bytes()
}
