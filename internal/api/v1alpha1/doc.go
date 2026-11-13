// Package v1alpha1 holds the Go types for the solaria.sh/v1alpha1 resources.
//
// These types are the source of truth for the published JSON Schema
// (schemas/v1alpha1). Doc comments on fields become schema descriptions, so
// they are written for people editing cards, not for Go readers. Run
// `make schema` after changing anything here.
package v1alpha1

const (
	// Group is the API group of every Solaria resource.
	Group = "solaria.sh"
	// Version is the API version these types implement.
	Version = "v1alpha1"
	// APIVersion is the full apiVersion value for these types.
	APIVersion = Group + "/" + Version
)

// Kinds known to this API version.
const (
	KindAgent       = "Agent"
	KindTarget      = "Target"
	KindEnvironment = "Environment"
	KindMcpServer   = "McpServer"
	KindVault       = "Vault"
)

// DefaultEnvironment names the built-in sandbox a target's agents get when
// no Environment is assigned to them. No Environment may take the name.
const DefaultEnvironment = "default"

// Kinds lists every kind in this API version, in documentation order.
var Kinds = []string{KindAgent, KindTarget, KindEnvironment, KindMcpServer, KindVault}
