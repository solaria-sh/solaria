package resolve

import (
	"github.com/solaria-sh/solaria/internal/api/v1alpha1"
)

// Format names the shape of Definition. Any change to what goes into a
// definition, or how it is normalized, changes every digest, so it needs a
// new Format (ADR 0008). The one exception is a new field that is omitted
// when empty, such as Vaults or Output: agents that don't use it keep their
// digests.
const Format = "solaria.sh/definition/v1"

// Definition is everything that makes up an agent's behavior, normalized so
// that equal agents produce equal JSON. It is the digest input, and what
// providers render for their host.
//
// It deliberately leaves out the agent's name and labels (the logical ID
// says who the agent is; the digest says what it is), the provider and
// placement (so the digest is the same on every host), and secret values
// (only reference names appear).
type Definition struct {
	Format      string `json:"format"`
	Description string `json:"description,omitempty"`
	// Instructions holds the instruction files' contents, in order, with
	// line endings normalized to \n.
	Instructions []string                  `json:"instructions"`
	Model        Model                     `json:"model"`
	Tools        Tools                     `json:"tools"`
	Skills       map[string]Skill          `json:"skills"`
	Permissions  Permissions               `json:"permissions"`
	Limits       Limits                    `json:"limits"`
	Providers    map[string]ProviderFields `json:"providers"`
	// Vaults the agent references, by name. Left out when there are none,
	// so agents without vaults hash as they did before Vaults existed.
	Vaults map[string]Vault `json:"vaults,omitempty"`
	// Output is the contract for the agent's final answer. Left out when the
	// agent has none, so other agents keep their digests (ADR 0019).
	Output *Output `json:"output,omitempty"`
	// Workspace is set only when the agent may not write to its workspace,
	// so read-write agents, the default, keep their digests (ADR 0017).
	Workspace *Workspace `json:"workspace,omitempty"`
}

// Workspace is what the agent may do to the folder it works on.
type Workspace struct {
	Access v1alpha1.Access `json:"access"`
}

// ReadOnlyWorkspace reports whether the agent may not change its
// workspace.
func (d Definition) ReadOnlyWorkspace() bool {
	return d.Workspace != nil && d.Workspace.Access == v1alpha1.AccessReadOnly
}

// workspace normalizes an access mode: read-write, the default, is left
// out.
func workspace(w *v1alpha1.WorkspaceSpec) *Workspace {
	if w == nil || w.Access != v1alpha1.AccessReadOnly {
		return nil
	}
	return &Workspace{Access: v1alpha1.AccessReadOnly}
}

// Vault is a referenced Vault: what each credential unlocks, never a value.
type Vault struct {
	// Credentials sorted by name.
	Credentials []Credential `json:"credentials"`
}

// Credential is one credential of a vault. Exactly one of MCPServer and Env
// is set.
type Credential struct {
	Name      string `json:"name"`
	SecretRef string `json:"secretRef"`
	MCPServer string `json:"mcpServer,omitempty"`
	Env       string `json:"env,omitempty"`
}

// Model is the resolved model binding.
type Model struct {
	Preferred string          `json:"preferred"`
	Fallback  []string        `json:"fallback"`
	Effort    v1alpha1.Effort `json:"effort,omitempty"`
}

// Tools are the tools the agent may use.
type Tools struct {
	// Builtin tool names, sorted.
	Builtin []string `json:"builtin"`
	// MCP servers by McpServer name.
	MCP map[string]MCPServer `json:"mcp"`
}

// MCPServer is a referenced McpServer with the agent's narrowing applied.
type MCPServer struct {
	Spec v1alpha1.McpServerSpec `json:"spec"`
	// AllowTools is sorted; nil means every tool the server offers.
	AllowTools []string `json:"allowTools"`
}

// Skill is an Agent Skills folder, by content.
type Skill struct {
	// Files maps each file's path inside the skill folder, with forward
	// slashes, to the digest of its contents.
	Files map[string]string `json:"files"`
}

// Permissions narrow tool use.
type Permissions struct {
	// Ask lists tools needing approval, sorted.
	Ask []string `json:"ask"`
}

// Limits bound each run.
type Limits struct {
	MaxTurns int `json:"maxTurns,omitempty"`
	// Timeout in Go duration form (1h30m0s), so 90m and 1h30m are equal.
	Timeout string `json:"timeout,omitempty"`
}

// ProviderFields are passthrough fields for one provider.
type ProviderFields = map[string]any
