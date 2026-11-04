// Package provider defines what a host provider implements (ADR 0006). The
// engine drives providers through this interface; it never talks to a host
// itself.
package provider

import (
	"context"

	"github.com/renantarouco/solaria/internal/diag"
	"github.com/renantarouco/solaria/internal/resolve"
	"github.com/renantarouco/solaria/internal/state"
)

// Provider deploys agents to one kind of host.
type Provider interface {
	// Name is the provider name environments use, such as gitagent.
	Name() string
	// Version is recorded in state with every entry it applies.
	Version() string
	// Placements lists where this provider can put a sandbox.
	Placements() []string
	// Capabilities says what the host accepts in a placement. The engine
	// checks agents against it before any host call.
	Capabilities(placement string) Capabilities
	// Validate reports provider-specific problems with an agent. Use it
	// for checks Capabilities can't express.
	Validate(ctx context.Context, desired *resolve.Agent) []diag.Diagnostic
	// Plan decides what to do for one agent. Most providers start from
	// DefaultPlan and adjust it: turn an update into a replace, or add
	// notes the person applying should read.
	Plan(ctx context.Context, desired *resolve.Agent, prior *state.Entry) (*Plan, error)
	// Apply carries out a create, update or replace and returns the new
	// binding. For a replace the engine has already destroyed the prior
	// object.
	Apply(ctx context.Context, p *Plan) (*Applied, error)
	// Read looks up an entry's host object, for refresh and drift.
	Read(ctx context.Context, e *state.Entry) (*Observed, error)
	// Destroy removes (or archives) an entry's host object.
	Destroy(ctx context.Context, e *state.Entry) error
}

// Options configure a provider for one run.
type Options struct {
	// Environment is the environment being planned or applied.
	Environment string
	// WorkDir is the project directory, for providers that keep files.
	WorkDir string
}

// Factory creates a provider.
type Factory func(Options) (Provider, error)

// Capabilities describe what a host accepts.
type Capabilities struct {
	// ModelProviders lists the model providers the host can run, such as
	// anthropic. Nil means any.
	ModelProviders []string
	// Fallback means the host can fall back to other models.
	Fallback bool
	// MCPTransports the host can use: stdio, streamable-http, sse.
	MCPTransports []string
	// SandboxImage means the placement accepts sandbox.image.
	SandboxImage bool
	// Approvals means permissions.ask is honored in unattended runs. When
	// false, those tools are denied instead, and plan warns.
	Approvals bool
	// Destroy says what destroy does on this host.
	Destroy DestroyKind
}

// DestroyKind is what destroying a host object means.
type DestroyKind string

// Destroy kinds.
const (
	// DestroyDelete removes the object.
	DestroyDelete DestroyKind = "delete"
	// DestroyArchive keeps the object but retires it, irreversibly.
	DestroyArchive DestroyKind = "archive"
)

// Transports.
const (
	TransportStdio          = "stdio"
	TransportStreamableHTTP = "streamable-http"
	TransportSSE            = "sse"
)

// Applied is the result of applying a plan.
type Applied struct {
	Binding    state.Binding
	RemoteHash string
}

// Observed is what Read found on the host.
type Observed struct {
	// Exists is false when the object is gone, for example deleted in a
	// console.
	Exists bool
	// RemoteHash of the object as it is now.
	RemoteHash string
}
