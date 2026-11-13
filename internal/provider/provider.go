// Package provider defines what a host provider implements (ADR 0006). The
// engine drives providers through this interface; it never talks to a host
// itself.
package provider

import (
	"context"
	"io"

	"github.com/solaria-sh/solaria/internal/api/v1alpha1"
	"github.com/solaria-sh/solaria/internal/diag"
	"github.com/solaria-sh/solaria/internal/resolve"
	"github.com/solaria-sh/solaria/internal/state"
)

// Provider deploys agents to one kind of host.
type Provider interface {
	// Name is the provider name targets use, such as gitagent.
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
	// Target is the target being planned or applied.
	Target string
	// WorkDir is the project directory, for providers that keep files.
	WorkDir string
	// Log receives progress from long operations, such as image builds.
	// Nil means discard.
	Log io.Writer
}

// Factory creates a provider.
type Factory func(Options) (Provider, error)

// How a placement gets an agent's repositories (Capabilities.Repositories).
const (
	RepositoriesClone  = "clone"
	RepositoriesGitHub = "github"
)

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
	// LocalWorkspace means runs work on a folder of the machine that starts
	// them, so the placement can take spec.inputs.workspace.path and keep a
	// read-only workspace from being written (ADR 0017).
	LocalWorkspace bool
	// Repositories says how the placement gets an agent's repositories (ADR
	// 0017): RepositoriesClone when its launcher clones them from any git
	// host before a run, RepositoriesGitHub when its sessions attach GitHub
	// repositories themselves, or nothing.
	Repositories string
	// ReadOnlyRepositories means the placement can keep a clone from being
	// written.
	ReadOnlyRepositories bool
	// Isolation lists the sandbox.isolation values the placement accepts,
	// such as srt on the process placement (ADR 0017).
	Isolation []v1alpha1.Isolation
	// Approvals means permissions.ask is honored in unattended runs. When
	// false, those tools are denied instead, and plan warns.
	Approvals bool
	// OutputSchema means the host makes the agent's final answer match
	// spec.output.schema (ADR 0019). When false, plan fails for an agent
	// that has one.
	OutputSchema bool
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
