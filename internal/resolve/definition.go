package resolve

import (
	"github.com/renantarouco/solaria/internal/api/v1alpha1"
)

// Format names the shape of Definition. Any change to what goes into a
// definition, or how it is normalized, changes every digest, so it needs a
// new Format (ADR 0008).
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
