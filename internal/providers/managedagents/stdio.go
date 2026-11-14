package managedagents

// MCP servers the self-hosted worker runs (ADR 0021). Anthropic connects to
// an agent's MCP servers itself, so it can only use servers with a remote.
// On self-hosted, the worker runs a server that has only stdio packages,
// and serves its tools as custom tools that the agent declares.

import (
	"encoding/json"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"

	"github.com/anthropics/anthropic-sdk-go/v2"

	"github.com/solaria-sh/solaria/internal/api/v1alpha1"
	"github.com/solaria-sh/solaria/internal/digest"
	"github.com/solaria-sh/solaria/internal/provider"
	"github.com/solaria-sh/solaria/internal/resolve"
	"github.com/solaria-sh/solaria/internal/state"
)

// Where the worker image installs MCP servers and describes them.
const (
	mcpRoot       = "/opt/solaria/mcp"
	mcpConfigPath = "/etc/solaria/mcp.json"
)

// outToolsPrefix starts the Environment binding outputs that hold each
// worker-run server's tools, as JSON: outToolsPrefix + server name.
const outToolsPrefix = "mcpTools."

// Limits on custom tools (the self-hosted custom tools reference).
const (
	maxTools = 256
)

var (
	toolNamePattern     = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)
	propertyNamePattern = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`)
)

// stdioPackage is the package the worker runs for a server with no remote
// on self-hosted, or nil when Anthropic connects to the server instead.
func stdioPackage(placement string, s v1alpha1.McpServerSpec) *v1alpha1.MCPPackage {
	if placement != PlacementSelfHosted {
		return nil
	}
	if url, _ := remote(s); url != "" {
		return nil
	}
	for i, p := range s.Packages {
		if p.Transport.Type == provider.TransportStdio && (p.RegistryType == v1alpha1.RegistryNPM || p.RegistryType == v1alpha1.RegistryPyPI) {
			return &s.Packages[i]
		}
	}
	return nil
}

// workerServer is an MCP server an Environment's worker runs.
type workerServer struct {
	Name    string
	Package v1alpha1.MCPPackage
	// Secrets are the secrets its package's variables name, sorted. Every
	// agent that uses the server is granted all of them, straight from the
	// secret source or through its vaults (ADR 0016), so the agents that
	// share a worker agree on what the server gets.
	Secrets []string
}

// key identifies what the server's tools were listed from: its package,
// arguments and variables.
func (w workerServer) key() string {
	dg, err := digest.Of(w.Package)
	if err != nil {
		return ""
	}
	return dg
}

// agentServers lists the worker servers an agent uses, sorted by name.
func agentServers(a *resolve.Agent) []workerServer {
	var out []workerServer
	for _, name := range slices.Sorted(maps.Keys(a.Definition.Tools.MCP)) {
		pkg := stdioPackage(a.Placement, a.Definition.Tools.MCP[name].Spec)
		if pkg == nil {
			continue
		}
		w := workerServer{Name: name, Package: *pkg}
		for _, kv := range pkg.EnvironmentVariables {
			if kv.SecretRef != "" {
				w.Secrets = append(w.Secrets, kv.SecretRef)
			}
		}
		slices.Sort(w.Secrets)
		w.Secrets = slices.Compact(w.Secrets)
		out = append(out, w)
	}
	return out
}

// workerServers collects the servers an Environment's worker runs: those
// of all its agents, sorted by name.
func workerServers(e *provider.Environment) []workerServer {
	byName := map[string]workerServer{}
	for _, a := range e.Agents {
		for _, w := range agentServers(a) {
			if _, seen := byName[w.Name]; !seen {
				byName[w.Name] = w
			}
		}
	}
	var out []workerServer
	for _, name := range slices.Sorted(maps.Keys(byName)) {
		out = append(out, byName[name])
	}
	return out
}

// workerConfig is the worker's /etc/solaria/mcp.json; the worker reads the
// same shape.
type workerConfig struct {
	Servers []workerConfigServer `json:"servers"`
}

type workerConfigServer struct {
	Name      string            `json:"name"`
	Runner    string            `json:"runner"`
	Dir       string            `json:"dir"`
	Package   string            `json:"package"`
	Args      []string          `json:"args,omitempty"`
	Env       map[string]string `json:"env,omitempty"`
	SecretEnv map[string]string `json:"secretEnv,omitempty"`
}

// renderServers writes the worker image's install steps and its MCP
// config. A secret reaches the container under its own name, so the
// config only names it.
func renderServers(b *strings.Builder, servers []workerServer) ([]byte, error) {
	cfg := workerConfig{Servers: []workerConfigServer{}}
	for _, w := range servers {
		dir := mcpRoot + "/" + w.Name
		p := w.Package
		c := workerConfigServer{Name: w.Name, Dir: dir, Package: p.Identifier}
		fmt.Fprintf(b, "# McpServer %s: %s %s@%s\n", w.Name, p.RegistryType, p.Identifier, p.Version)
		switch p.RegistryType {
		case v1alpha1.RegistryNPM:
			c.Runner = "npm"
			fmt.Fprintf(b, "RUN %s\n", jsonArray("npm", "install", "--prefix", dir, "--omit=dev", "--no-audit", "--no-fund", p.Identifier+"@"+p.Version))
		case v1alpha1.RegistryPyPI:
			c.Runner = "pypi"
			fmt.Fprintf(b, "RUN %s\n", jsonArray("python3", "-m", "venv", dir))
			fmt.Fprintf(b, "RUN %s\n", jsonArray(dir+"/bin/pip", "install", "--no-cache-dir", p.Identifier+"=="+p.Version))
		}
		for _, arg := range p.PackageArguments {
			if arg.Type == v1alpha1.ArgumentNamed {
				c.Args = append(c.Args, arg.Name)
			}
			c.Args = append(c.Args, arg.Value)
		}
		for _, kv := range p.EnvironmentVariables {
			switch {
			case kv.SecretRef == "":
				if c.Env == nil {
					c.Env = map[string]string{}
				}
				c.Env[kv.Name] = kv.Value
			case slices.Contains(w.Secrets, kv.SecretRef):
				if c.SecretEnv == nil {
					c.SecretEnv = map[string]string{}
				}
				c.SecretEnv[kv.Name] = kv.SecretRef
			}
		}
		cfg.Servers = append(cfg.Servers, c)
	}
	raw, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return nil, err
	}
	if len(servers) > 0 {
		fmt.Fprintf(b, "COPY mcp.json %s\n", mcpConfigPath)
	}
	return append(raw, '\n'), nil
}

func jsonArray(args ...string) string {
	raw, _ := json.Marshal(args)
	return string(raw)
}

// mcpTool is one tool a worker-run server lists.
type mcpTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
}

// toolList is a server's tools, as the Environment's binding records them,
// with the key of what they were listed from.
type toolList struct {
	Key   string    `json:"key"`
	Tools []mcpTool `json:"tools"`
}

// readToolLists reads the tool lists an Environment's binding records.
func readToolLists(e *state.EnvironmentEntry) map[string]toolList {
	out := map[string]toolList{}
	if e == nil {
		return out
	}
	for k, v := range e.Binding.Outputs {
		name, ok := strings.CutPrefix(k, outToolsPrefix)
		if !ok {
			continue
		}
		var tl toolList
		if json.Unmarshal([]byte(v), &tl) == nil {
			out[name] = tl
		}
	}
	return out
}

// toolsFor returns the tools of the worker servers an agent uses, from
// what its Environment lists, and the servers whose tools aren't listed
// for what the agent uses yet.
func (p *Provider) toolsFor(a *resolve.Agent) (map[string]toolList, []string) {
	out := map[string]toolList{}
	var missing []string
	for _, w := range agentServers(a) {
		tl, ok := p.tools[a.Environment][w.Name]
		if !ok || tl.Key != w.key() {
			missing = append(missing, w.Name)
			continue
		}
		out[w.Name] = tl
	}
	return out, missing
}

func (p *Provider) recordTools(env string, lists map[string]toolList) {
	if p.tools == nil {
		p.tools = map[string]map[string]toolList{}
	}
	p.tools[env] = lists
}

// customTools declares a worker-run server's tools on the agent as custom
// tools named <server>__<tool>, narrowed by allowTools.
func customTools(name string, srv resolve.MCPServer, tl toolList) []anthropic.BetaManagedAgentsCustomToolParams {
	var out []anthropic.BetaManagedAgentsCustomToolParams
	for _, t := range tl.Tools {
		if srv.AllowTools != nil && !slices.Contains(srv.AllowTools, t.Name) {
			continue
		}
		desc := t.Description
		if desc == "" {
			desc = "Tool " + t.Name + " of MCP server " + name + "."
		}
		schema := anthropic.BetaManagedAgentsCustomToolInputSchemaParam{ExtraFields: map[string]any{}}
		for k, v := range t.InputSchema {
			switch k {
			case "type":
			case "properties":
				schema.Properties, _ = v.(map[string]any)
			case "required":
				for _, r := range asList(v) {
					if s, ok := r.(string); ok {
						schema.Required = append(schema.Required, s)
					}
				}
			default:
				schema.ExtraFields[k] = v
			}
		}
		out = append(out, anthropic.BetaManagedAgentsCustomToolParams{Name: name + "__" + t.Name, Description: desc, InputSchema: schema})
	}
	return out
}

func asList(v any) []any {
	l, _ := v.([]any)
	return l
}

// checkTools reports the declarations of a server's tools that the host
// would refuse.
func checkTools(name string, srv resolve.MCPServer, tl toolList) []string {
	var problems []string
	have := map[string]bool{}
	for _, t := range tl.Tools {
		have[t.Name] = true
		if srv.AllowTools != nil && !slices.Contains(srv.AllowTools, t.Name) {
			continue
		}
		full := name + "__" + t.Name
		if !toolNamePattern.MatchString(full) {
			problems = append(problems, fmt.Sprintf("tool %s would be declared as %s, but a custom tool's name has 1 to 128 letters, digits, underscores and hyphens", t.Name, full))
		}
		if hasRef(t.InputSchema) {
			problems = append(problems, fmt.Sprintf("tool %s's input schema uses $ref, which custom tools don't take", t.Name))
		}
		for _, k := range []string{"oneOf", "anyOf", "allOf"} {
			if _, ok := t.InputSchema[k]; ok {
				problems = append(problems, fmt.Sprintf("tool %s's input schema has a top-level %s, which custom tools don't take", t.Name, k))
			}
		}
		props, _ := t.InputSchema["properties"].(map[string]any)
		for _, p := range slices.Sorted(maps.Keys(props)) {
			if !propertyNamePattern.MatchString(p) {
				problems = append(problems, fmt.Sprintf("tool %s's input property %q isn't 1 to 64 letters, digits, underscores, dots and hyphens", t.Name, p))
			}
		}
	}
	for _, t := range srv.AllowTools {
		if !have[t] {
			problems = append(problems, fmt.Sprintf("allowTools names %s, which the server doesn't list", t))
		}
	}
	return problems
}

func hasRef(v any) bool {
	switch v := v.(type) {
	case map[string]any:
		for k, x := range v {
			if k == "$ref" || hasRef(x) {
				return true
			}
		}
	case []any:
		for _, x := range v {
			if hasRef(x) {
				return true
			}
		}
	}
	return false
}
