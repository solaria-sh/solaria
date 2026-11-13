package process

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"path/filepath"
	"slices"
	"strings"

	"github.com/solaria-sh/solaria/internal/api/v1alpha1"
	"github.com/solaria-sh/solaria/internal/resolve"
)

// SRT is the command that runs a launcher inside Anthropic's
// sandbox-runtime when a target sets sandbox.isolation: srt (ADR
// 0017), and the npm package that installs it.
const (
	SRT        = "srt"
	SRTPackage = "@anthropic-ai/sandbox-runtime"
)

// SRTSettingsFile is the settings file a provider writes into its bundle
// for srt.
const SRTSettingsFile = "srt-settings.json"

// registries are the hosts each package registry downloads from, which a
// stdio MCP package needs when npx or uvx starts it.
var registries = map[v1alpha1.RegistryType][]string{
	v1alpha1.RegistryNPM:  {"registry.npmjs.org"},
	v1alpha1.RegistryPyPI: {"pypi.org", "files.pythonhosted.org"},
}

// packageManagerHosts are what sandbox.network.packageManagers allows: the
// registries of the package managers sandbox.packages names.
var packageManagerHosts = []string{
	"registry.npmjs.org",
	"pypi.org", "files.pythonhosted.org",
	"crates.io", "index.crates.io", "static.crates.io",
	"rubygems.org",
	"proxy.golang.org", "sum.golang.org",
}

// Isolated reports whether an agent's runs are wrapped in srt.
func Isolated(a *resolve.Agent) bool {
	return a.Sandbox != nil && a.Sandbox.Isolation == v1alpha1.IsolationSRT
}

// SRTSettings is srt's settings file. Every list is written, even empty:
// srt refuses a file that leaves one out.
type SRTSettings struct {
	Network    SRTNetwork    `json:"network"`
	Filesystem SRTFilesystem `json:"filesystem"`
}

// SRTNetwork limits the hosts a run reaches through srt's proxy.
type SRTNetwork struct {
	AllowedDomains []string `json:"allowedDomains"`
	DeniedDomains  []string `json:"deniedDomains"`
	// AllowLocalBinding lets the run reach this machine's loopback, such
	// as a telemetry collector on localhost.
	AllowLocalBinding bool `json:"allowLocalBinding,omitempty"`
}

// SRTFilesystem limits where a run writes. Reads stay open: the run reads
// the runtime, the bundle and the workspace from wherever they are.
type SRTFilesystem struct {
	DenyRead   []string `json:"denyRead"`
	AllowWrite []string `json:"allowWrite"`
	DenyWrite  []string `json:"denyWrite"`
}

// SRTFor builds the srt settings of an agent's runs. Writes go to srt's
// own temp folder, which holds the run's scratch directory, to cacheDir,
// which must be absolute, and to the workspace unless it's read-only, but
// never to a read-only repository's clone. The network is limited to
// modelHosts, the agent's remote MCP servers, the registries its stdio
// MCP packages come from, the telemetry endpoint, and sandbox.network.
func SRTFor(a *resolve.Agent, cacheDir string, modelHosts []string) SRTSettings {
	hosts, local := NeededHosts(a, modelHosts)
	// A run starts in its workspace unless the target binds one.
	ws := a.WorkspacePath()
	if ws == "" {
		ws = "."
	}
	writable := []string{cacheDir}
	if !a.Definition.ReadOnlyWorkspace() {
		writable = append(writable, ws)
	}
	// The launcher clones before it starts srt, so a read-only clone
	// already exists when srt denies writing it.
	unwritable := []string{}
	for _, r := range a.Definition.Repositories {
		if r.Access == v1alpha1.AccessReadOnly {
			unwritable = append(unwritable, filepath.ToSlash(filepath.Join(ws, "repositories", r.Name)))
		}
	}
	return SRTSettings{
		Network: SRTNetwork{
			AllowedDomains:    hosts,
			DeniedDomains:     []string{},
			AllowLocalBinding: local,
		},
		Filesystem: SRTFilesystem{
			DenyRead:   []string{},
			AllowWrite: writable,
			DenyWrite:  unwritable,
		},
	}
}

// NeededHosts lists the hosts an agent's runs need under limited
// networking, sorted, and reports whether one of them is this machine's
// loopback, which isn't reached by name.
func NeededHosts(a *resolve.Agent, modelHosts []string) (hosts []string, loopback bool) {
	add := func(h string) {
		if h == "" {
			return
		}
		if isLoopback(h) {
			loopback = true
			return
		}
		hosts = append(hosts, h)
	}
	for _, h := range modelHosts {
		add(h)
	}
	for _, s := range a.Definition.Tools.MCP {
		for _, r := range s.Spec.Remotes {
			add(urlHost(r.URL))
		}
		for _, p := range s.Spec.Packages {
			for _, h := range registries[p.RegistryType] {
				add(h)
			}
		}
	}
	if t := a.Env.Telemetry; t != nil {
		add(urlHost(t.Endpoint))
	}
	if sb := a.Sandbox; sb != nil && sb.Network != nil {
		for _, h := range sb.Network.Allow {
			add(h)
		}
		if sb.Network.PackageManagers {
			for _, h := range packageManagerHosts {
				add(h)
			}
		}
	}
	return sortedUnique(hosts), loopback
}

// urlHost is a URL's host name, without its port: srt then allows every
// port of it. A URL that doesn't parse, or names its host with a
// variable, gives nothing.
func urlHost(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || strings.ContainsAny(u.Hostname(), "{}") {
		return ""
	}
	h := u.Hostname()
	if strings.Contains(h, ":") {
		return "[" + h + "]"
	}
	return h
}

func isLoopback(host string) bool {
	h := host
	if hp, _, err := net.SplitHostPort(host); err == nil {
		h = hp
	}
	h = strings.ToLower(strings.Trim(h, "[]"))
	if h == "localhost" || strings.HasSuffix(h, ".localhost") {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

// JSON returns the settings as srt reads them, indented, so the same
// settings always give the same bytes.
func (s SRTSettings) JSON() []byte {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s)
	return b.Bytes()
}

// SRTArgv wraps a launcher's command in srt with the bundle's settings.
func SRTArgv(settings string, argv []string) []string {
	return append([]string{SRT, "--settings", settings, "--"}, argv...)
}

// srtDeps are the programs srt needs besides itself, per operating system.
var srtDeps = map[string][]string{
	"darwin": {"rg"},
	"linux":  {"bwrap", "socat", "rg"},
}

// checkSRT says why srt can't isolate runs on this machine, or nothing.
func checkSRT(goos string, lookPath func(string) (string, error)) string {
	deps, ok := srtDeps[goos]
	if !ok {
		return fmt.Sprintf("srt doesn't run on %s; use the docker placement instead", goos)
	}
	if _, err := lookPath(SRT); err != nil {
		return fmt.Sprintf("srt isn't on PATH; install it with npm install -g %s", SRTPackage)
	}
	var missing []string
	for _, d := range deps {
		if _, err := lookPath(d); err != nil {
			missing = append(missing, d)
		}
	}
	if len(missing) > 0 {
		slices.Sort(missing)
		return fmt.Sprintf("srt needs %s on PATH, which this machine doesn't have", strings.Join(missing, " and "))
	}
	return ""
}
