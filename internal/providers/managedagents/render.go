package managedagents

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/anthropics/anthropic-sdk-go/v2"
	"github.com/anthropics/anthropic-sdk-go/v2/packages/param"

	"github.com/solaria-sh/solaria/internal/api/v1alpha1"
	"github.com/solaria-sh/solaria/internal/diag"
	"github.com/solaria-sh/solaria/internal/resolve"
)

// Host limits (agent fields, from the API reference).
const (
	maxName        = 256
	maxDescription = 2048
	maxSystem      = 100_000
	maxMCPServers  = 20
)

// Metadata keys Solaria writes on agents and environments, so the Console
// and API show where an object comes from (ADR 0008).
const (
	metaLogicalID      = "solaria_logical_id"
	metaEnvironment    = "solaria_environment"
	metaSpecDigest     = "solaria_spec_digest"
	metaResolvedDigest = "solaria_resolved_digest"
)

// toolsetVersion is the built-in toolset agents get. Managed Agents runs
// the agent loop itself and can't be pinned to a version; the toolset is
// the part of its harness an agent's definition names.
const toolsetVersion = "agent_toolset_20260401"

// builtinTools maps Solaria's built-in tool names to agent_toolset_20260401.
var builtinTools = map[string]string{
	"shell":      "bash",
	"read":       "read",
	"write":      "write",
	"edit":       "edit",
	"glob":       "glob",
	"grep":       "grep",
	"web_fetch":  "web_fetch",
	"web_search": "web_search",
}

// ownedFields are the agent fields Solaria sets from the card. An agent's
// providers.managed-agents may set any other field, such as multiagent.
var ownedFields = []string{"description", "mcp_servers", "metadata", "model", "name", "skills", "system", "tools", "version"}

// skillRef is an uploaded skill version.
type skillRef struct{ ID, Version string }

// agentBody is everything Solaria sends for an agent. It converts to the
// create and update params, which differ only in their Go types.
type agentBody struct {
	Name, Description, System string
	Model                     anthropic.BetaManagedAgentsModelConfigParams
	Toolset                   *anthropic.BetaManagedAgentsAgentToolset20260401Params
	MCPToolsets               []anthropic.BetaManagedAgentsMCPToolsetParams
	MCPServers                []anthropic.BetaManagedAgentsURLMCPServerParams
	Skills                    []anthropic.BetaManagedAgentsSkillParamsUnion
	Metadata                  map[string]string
	// Passthrough are the agent's providers.managed-agents fields, sent as
	// they are.
	Passthrough map[string]any
}

// renderAgent turns a resolved agent into the body of its create or update
// request. skills holds each skill's uploaded version; a skill without one
// is rendered with an empty ID, which is how plans compare bodies before
// uploading.
func renderAgent(a *resolve.Agent, s settings, skills map[string]skillRef) agentBody {
	d := a.Definition
	body := agentBody{
		Name:        s.namePrefix + a.Name,
		Description: d.Description,
		System:      system(d.Instructions),
		Metadata: map[string]string{
			metaLogicalID:      a.ID(),
			metaEnvironment:    a.Environment,
			metaSpecDigest:     a.SpecDigest,
			metaResolvedDigest: a.ResolvedDigest,
		},
		Passthrough: d.Providers[Name],
	}
	_, model, _ := strings.Cut(d.Model.Preferred, ":")
	body.Model = anthropic.BetaManagedAgentsModelConfigParams{ID: model}
	if d.Model.Effort != "" {
		body.Model.Effort.OfBetaManagedAgentsModelConfigsEffortBetaManagedAgentsEffortLevel = param.NewOpt(string(d.Model.Effort))
	}

	asked := map[string]bool{}
	for _, t := range d.Permissions.Ask {
		asked[t] = true
	}
	if len(d.Tools.Builtin) > 0 {
		ts := &anthropic.BetaManagedAgentsAgentToolset20260401Params{
			DefaultConfig: anthropic.BetaManagedAgentsAgentToolsetDefaultConfigParams{Enabled: param.NewOpt(false)},
		}
		for _, t := range d.Tools.Builtin { // sorted by resolve
			ts.Configs = append(ts.Configs, toolConfig(builtinTools[t], asked[t]))
		}
		body.Toolset = ts
	}

	for _, name := range slices.Sorted(maps.Keys(d.Tools.MCP)) {
		srv := d.Tools.MCP[name]
		url, _ := remote(srv.Spec)
		body.MCPServers = append(body.MCPServers, anthropic.BetaManagedAgentsURLMCPServerParams{Name: name, URL: url})
		ts := anthropic.BetaManagedAgentsMCPToolsetParams{
			MCPServerName: name,
			DefaultConfig: anthropic.BetaManagedAgentsMCPToolsetDefaultConfigParams{
				Enabled:          param.NewOpt(srv.AllowTools == nil),
				PermissionPolicy: anthropic.BetaManagedAgentsMCPToolsetDefaultConfigParamsPermissionPolicyUnion{OfAlwaysAllow: &anthropic.BetaManagedAgentsAlwaysAllowPolicyParam{}},
			},
		}
		tools := srv.AllowTools
		if tools == nil {
			// Every tool is on; only asked ones need their own entry.
			for t := range asked {
				if server, tool, ok := strings.Cut(t, "/"); ok && server == name {
					tools = append(tools, tool)
				}
			}
			slices.Sort(tools)
		}
		for _, tool := range tools {
			c := anthropic.BetaManagedAgentsMCPToolConfigParams{Name: tool, Enabled: param.NewOpt(true)}
			if asked[name+"/"+tool] {
				c.PermissionPolicy.OfAlwaysAsk = &anthropic.BetaManagedAgentsAlwaysAskPolicyParam{}
			} else {
				c.PermissionPolicy.OfAlwaysAllow = &anthropic.BetaManagedAgentsAlwaysAllowPolicyParam{}
			}
			ts.Configs = append(ts.Configs, c)
		}
		body.MCPToolsets = append(body.MCPToolsets, ts)
	}

	for _, name := range slices.Sorted(maps.Keys(d.Skills)) {
		ref := skills[name]
		body.Skills = append(body.Skills, anthropic.BetaManagedAgentsSkillParamsUnion{
			OfCustom: &anthropic.BetaManagedAgentsCustomSkillParams{SkillID: ref.ID, Version: param.NewOpt(ref.Version)},
		})
	}
	return body
}

// system joins the instructions into the system prompt.
func system(instructions []string) string {
	parts := make([]string, len(instructions))
	for i, s := range instructions {
		parts[i] = strings.TrimRight(s, "\n")
	}
	return strings.Join(parts, "\n\n")
}

// remote picks the server's endpoint: streamable HTTP first, then SSE,
// which the host reaches through its fallback.
func remote(s v1alpha1.McpServerSpec) (string, *v1alpha1.MCPRemote) {
	for _, want := range []v1alpha1.RemoteType{v1alpha1.RemoteStreamableHTTP, v1alpha1.RemoteSSE} {
		for i, r := range s.Remotes {
			if r.Type == want {
				return r.URL, &s.Remotes[i]
			}
		}
	}
	return "", nil
}

// toolConfig configures one built-in tool. Each tool has its own Go type in
// the SDK, though they share a shape.
func toolConfig(name string, ask bool) anthropic.BetaManagedAgentsAgentToolConfigParamsUnion {
	on := param.NewOpt(true)
	allow, askp := &anthropic.BetaManagedAgentsAlwaysAllowPolicyParam{}, &anthropic.BetaManagedAgentsAlwaysAskPolicyParam{}
	if ask {
		allow = nil
	} else {
		askp = nil
	}
	switch name {
	case "bash":
		return anthropic.BetaManagedAgentsAgentToolConfigParamsUnion{OfBash: &anthropic.BetaManagedAgentsBashToolConfigParams{
			Enabled: on, PermissionPolicy: anthropic.BetaManagedAgentsBashToolConfigParamsPermissionPolicyUnion{OfAlwaysAllow: allow, OfAlwaysAsk: askp},
		}}
	case "read":
		return anthropic.BetaManagedAgentsAgentToolConfigParamsUnion{OfRead: &anthropic.BetaManagedAgentsReadToolConfigParams{
			Enabled: on, PermissionPolicy: anthropic.BetaManagedAgentsReadToolConfigParamsPermissionPolicyUnion{OfAlwaysAllow: allow, OfAlwaysAsk: askp},
		}}
	case "write":
		return anthropic.BetaManagedAgentsAgentToolConfigParamsUnion{OfWrite: &anthropic.BetaManagedAgentsWriteToolConfigParams{
			Enabled: on, PermissionPolicy: anthropic.BetaManagedAgentsWriteToolConfigParamsPermissionPolicyUnion{OfAlwaysAllow: allow, OfAlwaysAsk: askp},
		}}
	case "edit":
		return anthropic.BetaManagedAgentsAgentToolConfigParamsUnion{OfEdit: &anthropic.BetaManagedAgentsEditToolConfigParams{
			Enabled: on, PermissionPolicy: anthropic.BetaManagedAgentsEditToolConfigParamsPermissionPolicyUnion{OfAlwaysAllow: allow, OfAlwaysAsk: askp},
		}}
	case "glob":
		return anthropic.BetaManagedAgentsAgentToolConfigParamsUnion{OfGlob: &anthropic.BetaManagedAgentsGlobToolConfigParams{
			Enabled: on, PermissionPolicy: anthropic.BetaManagedAgentsGlobToolConfigParamsPermissionPolicyUnion{OfAlwaysAllow: allow, OfAlwaysAsk: askp},
		}}
	case "grep":
		return anthropic.BetaManagedAgentsAgentToolConfigParamsUnion{OfGrep: &anthropic.BetaManagedAgentsGrepToolConfigParams{
			Enabled: on, PermissionPolicy: anthropic.BetaManagedAgentsGrepToolConfigParamsPermissionPolicyUnion{OfAlwaysAllow: allow, OfAlwaysAsk: askp},
		}}
	case "web_fetch":
		return anthropic.BetaManagedAgentsAgentToolConfigParamsUnion{OfWebFetch: &anthropic.BetaManagedAgentsWebFetchToolConfigParams{
			Enabled: on, PermissionPolicy: anthropic.BetaManagedAgentsWebFetchToolConfigParamsPermissionPolicyUnion{OfAlwaysAllow: allow, OfAlwaysAsk: askp},
		}}
	default: // web_search; validate rejects unknown names
		return anthropic.BetaManagedAgentsAgentToolConfigParamsUnion{OfWebSearch: &anthropic.BetaManagedAgentsWebSearchToolConfigParams{
			Enabled: on, PermissionPolicy: anthropic.BetaManagedAgentsWebSearchToolConfigParamsPermissionPolicyUnion{OfAlwaysAllow: allow, OfAlwaysAsk: askp},
		}}
	}
}

// newParams returns the create request.
func (b agentBody) newParams() anthropic.BetaAgentNewParams {
	p := anthropic.BetaAgentNewParams{
		Name:       b.Name,
		Model:      b.Model,
		MCPServers: b.MCPServers,
		Skills:     b.Skills,
		Metadata:   b.Metadata,
	}
	if b.Description != "" {
		p.Description = param.NewOpt(b.Description)
	}
	p.System = param.NewOpt(b.System)
	if b.Toolset != nil {
		p.Tools = append(p.Tools, anthropic.BetaAgentNewParamsToolUnion{OfAgentToolset20260401: b.Toolset})
	}
	for i := range b.MCPToolsets {
		p.Tools = append(p.Tools, anthropic.BetaAgentNewParamsToolUnion{OfMCPToolset: &b.MCPToolsets[i]})
	}
	return p
}

// updateParams returns the update request. Lists are replaced whole on the
// host, and an omitted field is kept, so every field is sent: empty lists
// clear what the card no longer has.
func (b agentBody) updateParams(version int64) anthropic.BetaAgentUpdateParams {
	p := anthropic.BetaAgentUpdateParams{
		Name:        param.NewOpt(b.Name),
		Description: param.NewOpt(b.Description),
		System:      param.NewOpt(b.System),
		Model:       b.Model,
		Version:     param.NewOpt(version),
		MCPServers:  b.MCPServers,
		Skills:      b.Skills,
		Metadata:    b.Metadata,
		Tools:       []anthropic.BetaAgentUpdateParamsToolUnion{},
	}
	if p.MCPServers == nil {
		p.MCPServers = []anthropic.BetaManagedAgentsURLMCPServerParams{}
	}
	if p.Skills == nil {
		p.Skills = []anthropic.BetaManagedAgentsSkillParamsUnion{}
	}
	if b.Toolset != nil {
		p.Tools = append(p.Tools, anthropic.BetaAgentUpdateParamsToolUnion{OfAgentToolset20260401: b.Toolset})
	}
	for i := range b.MCPToolsets {
		p.Tools = append(p.Tools, anthropic.BetaAgentUpdateParamsToolUnion{OfMCPToolset: &b.MCPToolsets[i]})
	}
	return p
}

// validateAgent reports what the host can't take from an agent, beyond the
// engine's capability checks.
func validateAgent(a *resolve.Agent, s settings, where string) diag.List {
	var diags diag.List
	errorf := func(format string, args ...any) {
		diags = append(diags, diag.Errorf("", diag.Pos{}, "%s: %s", where, fmt.Sprintf(format, args...)))
	}
	warnf := func(format string, args ...any) {
		diags = append(diags, diag.Warnf("", diag.Pos{}, "%s: %s", where, fmt.Sprintf(format, args...)))
	}
	d := a.Definition
	if n := utf8.RuneCountInString(s.namePrefix + a.Name); n > maxName {
		errorf("the agent's name is %d characters with its prefix; the host takes %d", n, maxName)
	}
	if n := utf8.RuneCountInString(d.Description); n > maxDescription {
		errorf("description is %d characters; the host takes %d", n, maxDescription)
	}
	if n := utf8.RuneCountInString(system(d.Instructions)); n > maxSystem {
		errorf("the instructions are %d characters; the host takes %d", n, maxSystem)
	}
	if n := len(d.Tools.MCP); n > maxMCPServers {
		errorf("the agent uses %d MCP servers; the host takes %d", n, maxMCPServers)
	}
	for _, name := range slices.Sorted(maps.Keys(d.Tools.MCP)) {
		_, r := remote(d.Tools.MCP[name].Spec)
		if r == nil {
			continue
		}
		// A secret header comes from the agent's vaults, which validate
		// checks grant it, and ValidateVault checks the host can fill it.
		// Without vaults, nothing gives the host the value.
		var plain, secret []string
		for _, h := range r.Headers {
			if h.SecretRef == "" {
				plain = append(plain, h.Name)
			} else {
				secret = append(secret, h.Name)
			}
		}
		if len(plain) > 0 {
			warnf("McpServer %q sends %s, but Managed Agents connects to MCP servers without custom headers; it's left out", name, strings.Join(plain, ", "))
		}
		if len(secret) > 0 && len(d.Vaults) == 0 {
			warnf("McpServer %q sends %s from a secret, but Managed Agents agents hold no credentials: grant it through a Vault in spec.vaults, so sessions can attach it", name, strings.Join(secret, ", "))
		}
	}
	for _, t := range d.Permissions.Ask {
		server, tool, ok := strings.Cut(t, "/")
		if !ok {
			continue
		}
		if srv, used := d.Tools.MCP[server]; used && srv.AllowTools != nil && !slices.Contains(srv.AllowTools, tool) {
			warnf("permissions.ask lists %s, but allowTools doesn't enable it", t)
		}
	}
	if d.Limits.MaxTurns > 0 || d.Limits.Timeout != "" {
		warnf("limits aren't enforced on Managed Agents; a session runs until the agent finishes or is interrupted, and session budgets cap its cost")
	}
	for _, k := range slices.Sorted(maps.Keys(d.Providers[Name])) {
		if slices.Contains(ownedFields, k) {
			errorf("providers.%s.%s is set from the card; change the card instead", Name, k)
		}
	}
	return diags
}
