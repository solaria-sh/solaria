// Package process is the shared code of the process placement (ADR 0014):
// providers whose agent loop runs as a plain local process, such as
// claude-agent-sdk.
//
// Solaria never starts that process (ADR 0002). What this package builds is
// a description of the isolation every run gets, which a provider writes into
// its bundle as isolation.json, and the preludes (prelude/isolate.mjs and
// prelude/isolate.py) that the provider's launcher runs first to enforce it:
//
//   - an empty environment plus an allowlist: a minimal PATH, locale, proxy
//     and certificate settings, the variables the provider needs, and the
//     target's secrets, by name;
//   - a scratch HOME, config and temp directory per run, removed afterwards,
//     so user-level settings, skills, plugins, logins and memory are never
//     found;
//   - a cache directory for package downloads that persists across runs and
//     holds no configuration.
package process

import (
	"bytes"
	"encoding/json"
	"maps"
	"path/filepath"
	"slices"

	"github.com/solaria-sh/solaria/internal/api/v1alpha1"
	"github.com/solaria-sh/solaria/internal/diag"
	"github.com/solaria-sh/solaria/internal/resolve"
)

// Placement is the placement's name.
const Placement = "process"

// Isolation is what a launcher needs to start a hermetic run. Its JSON form
// is the contract with the preludes: change both together.
type Isolation struct {
	// Binaries the run needs on PATH, such as node or npx. The launcher looks
	// each up on the caller's PATH and builds PATH from their folders, the
	// runtime's own folder and the system folders. A missing one fails the
	// run before it starts.
	Binaries []string `json:"binaries"`
	// Path lists folders PATH gets after the binaries' folders, for a
	// placement that knows the machine the run starts on, such as the
	// folders an image installs its packages into.
	Path []string `json:"path,omitempty"`
	// Pass lists variables copied from the caller's environment when set.
	Pass []string `json:"pass"`
	// Secrets lists the names of the secrets the run is given. Their values
	// come from Dotenv when it's set, otherwise from the caller's
	// environment. A missing secret fails the run before it starts.
	Secrets []string `json:"secrets"`
	// SecretEnv maps variables to the secrets that hold their values, for a
	// Vault credential whose variable is named differently from its secret.
	// The secret is read as for Secrets, and the run sees only the variable.
	SecretEnv map[string]string `json:"secretEnv,omitempty"`
	// Dotenv is the absolute path of the target's dotenv file.
	Dotenv string `json:"dotenv,omitempty"`
	// Repositories are cloned into repositories/<name> in the workspace
	// before the run starts (ADR 0017). Their tokens are read like
	// Secrets, and only git sees them.
	Repositories []Clone `json:"repositories,omitempty"`
	// Set lists variables set to fixed values.
	Set map[string]string `json:"set"`
	// Scratch maps variables to folders under the run's scratch directory,
	// which is created for each run and removed after it.
	Scratch map[string]string `json:"scratch"`
	// Cache maps variables to folders under the bundle's cache directory,
	// which persists across runs. Only package caches belong here.
	Cache map[string]string `json:"cache"`
}

// Clone is a repository the launcher clones before a run.
type Clone struct {
	Name  string `json:"name"`
	URL   string `json:"url"`
	Ref   string `json:"ref,omitempty"`
	Depth int    `json:"depth,omitempty"`
	// SecretRef names the secret holding the token that authenticates the
	// clone; empty for a public repository.
	SecretRef string `json:"secretRef,omitempty"`
}

// Needs is what a provider adds to the base isolation.
type Needs struct {
	Binaries []string
	Path     []string
	Pass     []string
	Set      map[string]string
	Scratch  map[string]string
	Cache    map[string]string
}

// basePass are the variables every run may see: locale, proxies and
// certificates, which affect how the machine reaches the network but not
// what the agent is, and the variables Windows programs need to start.
var basePass = []string{
	"LANG", "LC_ALL", "LC_CTYPE", "TZ",
	"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "ALL_PROXY",
	"http_proxy", "https_proxy", "no_proxy", "all_proxy",
	"SSL_CERT_FILE", "SSL_CERT_DIR", "NODE_EXTRA_CA_CERTS", "REQUESTS_CA_BUNDLE",
	"SYSTEMROOT", "SYSTEMDRIVE", "WINDIR", "COMSPEC", "PATHEXT",
	"PROGRAMFILES", "OS", "PROCESSOR_ARCHITECTURE", "NUMBER_OF_PROCESSORS",
}

// baseScratch points every home, config and temp variable that POSIX,
// XDG and Windows programs read at the scratch directory. Setting the
// Windows ones on POSIX, and the reverse, is harmless.
var baseScratch = map[string]string{
	"HOME":            "home",
	"USERPROFILE":     "home",
	"XDG_CONFIG_HOME": "home/.config",
	"XDG_DATA_HOME":   "home/.local/share",
	"XDG_STATE_HOME":  "home/.local/state",
	"XDG_CACHE_HOME":  "home/.cache",
	"APPDATA":         "home/AppData/Roaming",
	"LOCALAPPDATA":    "home/AppData/Local",
	"TMPDIR":          "tmp",
	"TEMP":            "tmp",
	"TMP":             "tmp",
}

// baseCache keeps package downloads for stdio MCP servers across runs.
var baseCache = map[string]string{
	"npm_config_cache": "npm",
	"UV_CACHE_DIR":     "uv",
	"PIP_CACHE_DIR":    "pip",
}

// For builds the isolation of one agent's runs: the base allowlist, the
// provider's needs, and the secrets the agent is granted.
func For(a *resolve.Agent, needs Needs) Isolation {
	iso := Isolation{
		Binaries: sortedUnique(needs.Binaries),
		Path:     needs.Path,
		Pass:     sortedUnique(append(slices.Clone(basePass), needs.Pass...)),
		Secrets:  []string{},
		Set:      merge(nil, needs.Set),
		Scratch:  merge(baseScratch, needs.Scratch),
		Cache:    merge(baseCache, needs.Cache),
	}
	for v, secret := range a.SecretVars() {
		if v == secret {
			iso.Secrets = append(iso.Secrets, v)
			continue
		}
		if iso.SecretEnv == nil {
			iso.SecretEnv = map[string]string{}
		}
		iso.SecretEnv[v] = secret
	}
	slices.Sort(iso.Secrets)
	for _, r := range a.Repositories() {
		iso.Repositories = append(iso.Repositories, Clone{Name: r.Name, URL: r.URL, Ref: r.Ref, Depth: r.Depth, SecretRef: r.SecretRef})
	}
	if s := a.Env.Secrets; s != nil && s.Source == v1alpha1.SecretsDotenv {
		p := s.Path
		if p == "" {
			p = ".env"
		}
		abs, err := filepath.Abs(filepath.Join(a.EnvDir, filepath.FromSlash(p)))
		if err != nil {
			abs = filepath.Join(a.EnvDir, filepath.FromSlash(p))
		}
		iso.Dotenv = abs
	}
	return iso
}

// JSON returns the isolation as the preludes read it: indented, with sorted
// keys, so the same isolation always gives the same bytes.
func (iso Isolation) JSON() []byte {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	// Encoding these types can't fail.
	_ = enc.Encode(iso)
	return b.Bytes()
}

// Machine is what Validate needs to know about the machine runs start on.
type Machine struct {
	// GOOS is its operating system.
	GOOS string
	// Managed lists the Claude Code managed settings files found on it.
	Managed []string
	// LookPath finds a program on its PATH, as exec.LookPath does.
	LookPath func(string) (string, error)
}

// Validate returns what every process placement says about an agent's
// target: what a local process can't enforce, managed settings that
// reach every Claude Code process on this machine, and, with
// sandbox.isolation: srt, why srt can't isolate runs here.
func Validate(a *resolve.Agent, m Machine) diag.List {
	var diags diag.List
	warn := func(format string, args ...any) {
		diags = append(diags, diag.Warnf("", diag.Pos{}, format, args...))
	}
	sb := a.Env.Sandbox
	limited := sb == nil || sb.Network == nil || sb.Network.Mode != v1alpha1.NetworkUnrestricted
	if sb != nil && sb.Packages != nil {
		warn("sandbox.packages aren't installed on the process placement: the run uses the tools this machine has")
	}
	switch {
	case Isolated(a):
		if !limited {
			diags = append(diags, diag.Errorf("", diag.Pos{}, "sandbox.isolation srt limits the network to the hosts the agent needs, so it can't be combined with sandbox.network.mode unrestricted; list the extra hosts in sandbox.network.allow"))
		}
		if why := checkSRT(m.GOOS, m.LookPath); why != "" {
			diags = append(diags, diag.Errorf("", diag.Pos{}, "sandbox.isolation srt can't isolate runs on this machine: %s", why))
		}
		if _, loopback := NeededHosts(a, nil); loopback && m.GOOS == "linux" {
			warn("srt runs the agent in its own network namespace on Linux, so hosts on this machine's loopback, such as a telemetry collector on localhost, may be unreachable")
		}
	case limited:
		warn("sandbox.network is limited, but the process placement doesn't enforce it: the run can reach any host this machine can; set sandbox.isolation: srt to enforce it")
	}
	if a.Definition.ReadOnlyWorkspace() && !Isolated(a) {
		diags = append(diags, diag.Errorf("", diag.Pos{}, "workspace.access is read-only, and a process run can write anywhere its user can; set sandbox.isolation: srt or use the docker placement to keep the workspace read-only"))
	}
	if !Isolated(a) {
		for _, r := range a.Repositories() {
			if r.Access == v1alpha1.AccessReadOnly {
				diags = append(diags, diag.Errorf("", diag.Pos{}, "repository %s is read-only, and a process run can write anywhere its user can; set sandbox.isolation: srt to keep its clone read-only", r.Name))
			}
		}
	}
	if len(a.Definition.Repositories) > 0 && m.LookPath != nil {
		if _, err := m.LookPath("git"); err != nil {
			diags = append(diags, diag.Errorf("", diag.Pos{}, "repositories are cloned with git, which isn't on PATH"))
		}
	}
	if sb != nil && sb.Resources != nil && (sb.Resources.Memory != "" || sb.Resources.Disk != "") {
		warn("sandbox.resources aren't enforced on the process placement")
	}
	for _, f := range m.Managed {
		warn("managed settings in %s apply to every Claude Code process on this machine and can't be turned off on the process placement; use the docker placement to keep them out", f)
	}
	return diags
}

func sortedUnique(xs []string) []string {
	out := slices.Clone(xs)
	if out == nil {
		return []string{}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

func merge(base, extra map[string]string) map[string]string {
	out := map[string]string{}
	maps.Copy(out, base)
	maps.Copy(out, extra)
	return out
}
