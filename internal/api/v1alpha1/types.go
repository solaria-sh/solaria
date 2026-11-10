package v1alpha1

import (
	"path/filepath"
	"strings"
)

// ObjectMeta identifies a resource.
type ObjectMeta struct {
	// Unique name per kind. Lowercase letters, digits and
	// hyphens, starting and ending with a letter or digit, at most 63
	// characters.
	Name string `json:"name" jsonschema:"required,minLength=1,maxLength=63,pattern=^[a-z0-9]([-a-z0-9]*[a-z0-9])?$"`
	// Labels for selecting resources, for example from an Environment's
	// agents.selector.
	Labels map[string]string `json:"labels,omitempty"`
}

// Agent declares what an agent is: its instructions, model, tools, skills and
// limits. Where it runs is decided by an Environment.
type Agent struct {
	// API version of this resource.
	APIVersion string `json:"apiVersion" jsonschema:"required"`
	// Kind of this resource.
	Kind string `json:"kind" jsonschema:"required"`
	// Identifies the agent.
	Metadata ObjectMeta `json:"metadata" jsonschema:"required"`
	// Describes the agent.
	Spec AgentSpec `json:"spec" jsonschema:"required"`
}

// AgentSpec describes an agent independently of the host it runs on.
type AgentSpec struct {
	// What the agent is for. Hosts that list agents show it.
	Description string `json:"description,omitempty"`
	// Markdown files, relative to this file, joined in order to form the
	// system prompt.
	Instructions []string `json:"instructions" jsonschema:"required,minItems=1"`
	// The model the agent uses.
	Model ModelSpec `json:"model" jsonschema:"required"`
	// Tools the agent may use.
	Tools *ToolsSpec `json:"tools,omitempty"`
	// Paths to Agent Skills folders, relative to this file. Each folder
	// holds a SKILL.md.
	Skills []string `json:"skills,omitempty" jsonschema:"uniqueItems=true"`
	// Narrows how the agent may use its tools.
	Permissions *PermissionsSpec `json:"permissions,omitempty"`
	// Bounds each run of the agent.
	Limits *LimitsSpec `json:"limits,omitempty"`
	// Fields passed unchanged to one provider, keyed by provider name. Using
	// them makes the agent less portable, and validate reports it.
	Providers map[string]map[string]any `json:"providers,omitempty"`
}

// ModelSpec selects a model as provider:model strings.
type ModelSpec struct {
	// Preferred model, as provider:model, for example
	// anthropic:claude-sonnet-5-5.
	Preferred string `json:"preferred" jsonschema:"required,pattern=^[a-z][a-z0-9-]*:[^ ]+$"`
	// Fallback models, tried in order on hosts that support fallback.
	Fallback []ModelRef `json:"fallback,omitempty"`
	// How much reasoning the model should spend. Providers map it to the
	// closest setting their host has.
	Effort Effort `json:"effort,omitempty"`
}

// ModelRef is a model as provider:model.
type ModelRef string

// Effort is a reasoning effort level.
type Effort string

// Effort levels.
const (
	EffortLow    Effort = "low"
	EffortMedium Effort = "medium"
	EffortHigh   Effort = "high"
	EffortXHigh  Effort = "xhigh"
	EffortMax    Effort = "max"
)

// ToolsSpec lists the tools an agent may use. Tools not listed are not
// available.
type ToolsSpec struct {
	// Builtin tools, by Solaria's host-neutral names. Providers map them to
	// their host's own tools.
	Builtin []BuiltinTool `json:"builtin,omitempty" jsonschema:"uniqueItems=true"`
	// MCP servers the agent uses, by reference to McpServer resources.
	MCP []MCPToolRef `json:"mcp,omitempty"`
}

// BuiltinTool is a host-neutral built-in tool name.
type BuiltinTool string

// Built-in tools.
const (
	ToolShell     BuiltinTool = "shell"
	ToolRead      BuiltinTool = "read"
	ToolWrite     BuiltinTool = "write"
	ToolEdit      BuiltinTool = "edit"
	ToolGlob      BuiltinTool = "glob"
	ToolGrep      BuiltinTool = "grep"
	ToolWebFetch  BuiltinTool = "web_fetch"
	ToolWebSearch BuiltinTool = "web_search"
)

// BuiltinTools lists every built-in tool name.
var BuiltinTools = []BuiltinTool{
	ToolShell, ToolRead, ToolWrite, ToolEdit,
	ToolGlob, ToolGrep, ToolWebFetch, ToolWebSearch,
}

// MCPToolRef references an McpServer and narrows which of its tools the agent
// may call.
type MCPToolRef struct {
	// Name of an McpServer resource.
	Ref string `json:"ref" jsonschema:"required,minLength=1"`
	// The server's tools this agent may call. When omitted, every tool the
	// server offers is allowed.
	AllowTools []string `json:"allowTools,omitempty" jsonschema:"minItems=1,uniqueItems=true"`
}

// PermissionsSpec narrows how the agent may use its tools.
type PermissionsSpec struct {
	// Tools that need a person's approval before each call: a builtin name,
	// or server/tool for an MCP tool. On hosts without
	// approvals in unattended runs, these tools are denied, and plan says so.
	Ask []string `json:"ask,omitempty" jsonschema:"uniqueItems=true"`
}

// LimitsSpec bounds each run of an agent.
type LimitsSpec struct {
	// Most model calls in one run.
	MaxTurns int `json:"maxTurns,omitempty" jsonschema:"minimum=1"`
	// Longest a run may take, as a duration such as 30m or 1h30m.
	Timeout string `json:"timeout,omitempty" jsonschema:"pattern=^([0-9]+(h|m|s))+$"`
}

// Environment binds agents to a provider and placement, and says how their
// sandbox, secrets and telemetry are set up there.
type Environment struct {
	// API version of this resource.
	APIVersion string `json:"apiVersion" jsonschema:"required"`
	// Kind of this resource.
	Kind string `json:"kind" jsonschema:"required"`
	// Identifies the environment.
	Metadata ObjectMeta `json:"metadata" jsonschema:"required"`
	// Describes the environment.
	Spec EnvironmentSpec `json:"spec" jsonschema:"required"`
}

// EnvironmentSpec describes where and how agents run.
type EnvironmentSpec struct {
	// Who runs the agent loop, for example gitagent or managed-agents.
	Provider string `json:"provider" jsonschema:"required,pattern=^[a-z0-9]([-a-z0-9]*[a-z0-9])?$"`
	// Where the provider's sandbox lives, for example docker, cloud or
	// self-hosted.
	Placement string `json:"placement" jsonschema:"required,pattern=^[a-z0-9]([-a-z0-9]*[a-z0-9])?$"`
	// Which agents this environment receives. When omitted, it receives
	// every agent.
	Agents *AgentSelection `json:"agents,omitempty"`
	// The machine the agents' tools run on.
	Sandbox *SandboxSpec `json:"sandbox,omitempty"`
	// Where secret values are read from when applying.
	Secrets *SecretsSpec `json:"secrets,omitempty"`
	// Where agents send OpenTelemetry data.
	Telemetry *TelemetrySpec `json:"telemetry,omitempty"`
	// Changes to agents in this environment only, keyed by agent name.
	Overrides map[string]AgentOverride `json:"overrides,omitempty"`
	// Settings for the environment's provider, keyed by provider name, such
	// as the platform gitagent builds images for. Each provider documents
	// its own settings. They describe the host, not the agents, so they
	// aren't part of any agent's digest.
	Providers map[string]map[string]any `json:"providers,omitempty"`
}

// IsDockerfilePath tells a Dockerfile path in sandbox.image from an image
// reference such as ghcr.io/org/image:tag.
func IsDockerfilePath(image string) bool {
	if image == "" {
		return false
	}
	base := strings.ToLower(filepath.Base(image))
	return strings.HasPrefix(image, "./") || strings.HasPrefix(image, "../") || filepath.IsAbs(image) ||
		base == "dockerfile" || strings.HasSuffix(base, ".dockerfile")
}

// AgentSelection selects agents.
type AgentSelection struct {
	// Matches agents by label.
	Selector LabelSelector `json:"selector" jsonschema:"required"`
}

// LabelSelector matches resources by label.
type LabelSelector struct {
	// Selects resources that have all these labels.
	MatchLabels map[string]string `json:"matchLabels" jsonschema:"required,minProperties=1"`
}

// SandboxSpec describes a sandbox in a form every placement understands.
type SandboxSpec struct {
	// Packages to install, by package manager, in each manager's own pin
	// syntax.
	Packages *PackagesSpec `json:"packages,omitempty"`
	// Outbound network access. When omitted, access is limited to
	// the hosts the agents need.
	Network *NetworkSpec `json:"network,omitempty"`
	// Memory and disk caps, where the placement allows them.
	Resources *ResourcesSpec `json:"resources,omitempty"`
	// Replaces the generated sandbox image: a container image reference, or
	// a path to a Dockerfile relative to this file. Only container
	// placements accept it.
	Image string `json:"image,omitempty" jsonschema:"minLength=1"`
}

// PackagesSpec lists packages per package manager.
type PackagesSpec struct {
	// APT packages.
	APT []string `json:"apt,omitempty" jsonschema:"uniqueItems=true"`
	// Cargo crates.
	Cargo []string `json:"cargo,omitempty" jsonschema:"uniqueItems=true"`
	// Ruby gems.
	Gem []string `json:"gem,omitempty" jsonschema:"uniqueItems=true"`
	// Go modules, installed with go install.
	Go []string `json:"go,omitempty" jsonschema:"uniqueItems=true"`
	// npm packages, installed globally.
	NPM []string `json:"npm,omitempty" jsonschema:"uniqueItems=true"`
	// pip packages.
	Pip []string `json:"pip,omitempty" jsonschema:"uniqueItems=true"`
}

// NetworkSpec controls outbound network access.
type NetworkSpec struct {
	// limited (only allowed hosts) or unrestricted.
	Mode NetworkMode `json:"mode,omitempty"`
	// Extra hosts the sandbox may reach in limited mode.
	Allow []string `json:"allow,omitempty" jsonschema:"uniqueItems=true"`
	// Allows the package managers' registries in limited mode.
	PackageManagers bool `json:"packageManagers,omitempty"`
}

// NetworkMode is a sandbox network mode.
type NetworkMode string

// Network modes.
const (
	NetworkLimited      NetworkMode = "limited"
	NetworkUnrestricted NetworkMode = "unrestricted"
)

// ResourcesSpec caps sandbox resources.
type ResourcesSpec struct {
	// Memory cap, such as 8Gi.
	Memory string `json:"memory,omitempty" jsonschema:"pattern=^[0-9]+(Ki|Mi|Gi|Ti)?$"`
	// Disk cap, such as 10Gi.
	Disk string `json:"disk,omitempty" jsonschema:"pattern=^[0-9]+(Ki|Mi|Gi|Ti)?$"`
}

// SecretsSpec says where secret values come from.
type SecretsSpec struct {
	// env (the process environment) or dotenv (a .env file).
	Source SecretSource `json:"source" jsonschema:"required"`
	// Path of the dotenv file, relative to this file. Defaults to .env.
	Path string `json:"path,omitempty" jsonschema:"minLength=1"`
}

// SecretSource is where secret values are read from.
type SecretSource string

// Secret sources.
const (
	SecretsEnv    SecretSource = "env"
	SecretsDotenv SecretSource = "dotenv"
)

// TelemetrySpec says where agents send OpenTelemetry data.
type TelemetrySpec struct {
	// URL of an OTLP receiver, such as http://localhost:4318.
	Endpoint string `json:"endpoint" jsonschema:"required,format=uri"`
	// OTLP protocol. Defaults to http/protobuf.
	Protocol OTLPProtocol `json:"protocol,omitempty"`
	// Headers sent with every export.
	Headers []KeyValue `json:"headers,omitempty"`
}

// OTLPProtocol is an OTLP transport protocol.
type OTLPProtocol string

// OTLP protocols.
const (
	OTLPGRPC         OTLPProtocol = "grpc"
	OTLPHTTPProtobuf OTLPProtocol = "http/protobuf"
)

// AgentOverride changes an agent in one environment.
type AgentOverride struct {
	// Model fields to replace.
	Model *ModelOverride `json:"model,omitempty"`
	// Tools to take away in this environment. An override can only narrow
	// the agent's tools: validate fails on one that adds a tool.
	Tools *ToolsOverride `json:"tools,omitempty"`
	// Permissions to tighten in this environment.
	Permissions *PermissionsOverride `json:"permissions,omitempty"`
	// Limits to replace.
	Limits *LimitsSpec `json:"limits,omitempty"`
	// Providers fields to merge over the agent's own.
	Providers map[string]map[string]any `json:"providers,omitempty"`
}

// ToolsOverride narrows an agent's tools in one environment, for example so
// a shadow stage only reads.
type ToolsOverride struct {
	// Built-in tools to keep, out of the agent's spec.tools.builtin. Tools
	// the agent has but this list leaves out are removed here; an empty list
	// removes them all. Naming a tool the agent doesn't have is an error.
	Builtin []BuiltinTool `json:"builtin,omitempty" jsonschema:"uniqueItems=true"`
	// MCP servers whose tools to narrow. Servers the agent uses but this
	// list doesn't name are unchanged; a server the agent doesn't use is an
	// error.
	MCP []MCPToolOverride `json:"mcp,omitempty"`
}

// MCPToolOverride narrows the tools of one MCP server the agent uses.
type MCPToolOverride struct {
	// Name of an McpServer the agent references in spec.tools.mcp.
	Ref string `json:"ref" jsonschema:"required,minLength=1"`
	// The server's tools this agent may call here. When the agent has its
	// own allowTools for the server, this must be a subset of it.
	AllowTools []string `json:"allowTools" jsonschema:"required,minItems=1,uniqueItems=true"`
}

// PermissionsOverride tightens an agent's permissions in one environment.
type PermissionsOverride struct {
	// Tools to add to the agent's permissions.ask: a builtin name, or
	// server/tool for an MCP tool. The agent's own entries stay.
	Ask []string `json:"ask,omitempty" jsonschema:"uniqueItems=true"`
}

// ModelOverride replaces some of an agent's model fields.
type ModelOverride struct {
	// Preferred model, as provider:model.
	Preferred string `json:"preferred,omitempty" jsonschema:"pattern=^[a-z][a-z0-9-]*:[^ ]+$"`
	// Fallback models, replacing the agent's list.
	Fallback []ModelRef `json:"fallback,omitempty"`
	// Effort level.
	Effort Effort `json:"effort,omitempty"`
}

// McpServer is a reusable MCP server definition, in a subset of the MCP
// Registry server.json shape.
type McpServer struct {
	// API version of this resource.
	APIVersion string `json:"apiVersion" jsonschema:"required"`
	// Kind of this resource.
	Kind string `json:"kind" jsonschema:"required"`
	// Identifies the server.
	Metadata ObjectMeta `json:"metadata" jsonschema:"required"`
	// Describes how to reach the server.
	Spec McpServerSpec `json:"spec" jsonschema:"required"`
}

// McpServerSpec offers one or more ways to reach an MCP server. Each provider
// picks one its placement supports.
type McpServerSpec struct {
	// Description of the server.
	Description string `json:"description,omitempty"`
	// Packages that run the server locally, inside the sandbox, over stdio.
	Packages []MCPPackage `json:"packages,omitempty" jsonschema:"minItems=1"`
	// Endpoints to connect to.
	Remotes []MCPRemote `json:"remotes,omitempty" jsonschema:"minItems=1"`
}

// MCPPackage is a server package to install and run.
type MCPPackage struct {
	// Package registry: npm or pypi.
	RegistryType RegistryType `json:"registryType" jsonschema:"required"`
	// Package name in its registry.
	Identifier string `json:"identifier" jsonschema:"required,minLength=1"`
	// Exact package version.
	Version string `json:"version" jsonschema:"required,minLength=1"`
	// Transport the package speaks.
	Transport MCPPackageTransport `json:"transport" jsonschema:"required"`
	// Arguments passed to the server when it starts.
	PackageArguments []MCPArgument `json:"packageArguments,omitempty"`
	// Environment variables set for the server process.
	EnvironmentVariables []KeyValue `json:"environmentVariables,omitempty"`
}

// RegistryType is a package registry.
type RegistryType string

// Package registries supported in v1alpha1.
const (
	RegistryNPM  RegistryType = "npm"
	RegistryPyPI RegistryType = "pypi"
)

// MCPPackageTransport is the transport a package speaks.
type MCPPackageTransport struct {
	// Always stdio.
	Type string `json:"type" jsonschema:"required,enum=stdio"`
}

// MCPArgument is one argument passed to a server package.
type MCPArgument struct {
	// positional or named.
	Type ArgumentType `json:"type" jsonschema:"required"`
	// Name of a named argument, such as --port.
	Name string `json:"name,omitempty" jsonschema:"minLength=1"`
	// Value of the argument.
	Value string `json:"value" jsonschema:"required"`
}

// ArgumentType is how an argument is passed.
type ArgumentType string

// Argument types.
const (
	ArgumentPositional ArgumentType = "positional"
	ArgumentNamed      ArgumentType = "named"
)

// MCPRemote is a server endpoint.
type MCPRemote struct {
	// Remote transport: streamable-http or sse.
	Type RemoteType `json:"type" jsonschema:"required"`
	// URL of the endpoint.
	URL string `json:"url" jsonschema:"required,format=uri"`
	// Headers sent with every request.
	Headers []KeyValue `json:"headers,omitempty"`
}

// RemoteType is a remote MCP transport.
type RemoteType string

// Remote transports.
const (
	RemoteStreamableHTTP RemoteType = "streamable-http"
	RemoteSSE            RemoteType = "sse"
)

// KeyValue is a named value, given inline or as a reference to a secret. Set
// exactly one of value and secretRef.
type KeyValue struct {
	// Name of the header or variable.
	Name string `json:"name" jsonschema:"required,minLength=1"`
	// Value, when it isn't secret.
	Value string `json:"value,omitempty"`
	// Name of a secret in the environment's secret source, in
	// UPPER_SNAKE_CASE. Its value is never stored by Solaria.
	SecretRef string `json:"secretRef,omitempty" jsonschema:"pattern=^[A-Z_][A-Z0-9_]*$"`
}
