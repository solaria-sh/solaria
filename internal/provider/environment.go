package provider

import (
	"context"

	"github.com/renantarouco/solaria/internal/api/v1alpha1"
	"github.com/renantarouco/solaria/internal/diag"
	"github.com/renantarouco/solaria/internal/resolve"
	"github.com/renantarouco/solaria/internal/state"
)

// EnvironmentHost is implemented by providers whose host keeps an object
// for the environment itself, which all its agents share (ADR 0012). A
// Managed Agents environment is one: the sandbox template every session
// starts from, and on self-hosted the work queue a worker polls.
//
// The engine plans this object together with the agents. It creates or
// updates it before them and deletes it after them.
type EnvironmentHost interface {
	// ValidateEnvironment reports problems with the environment as this
	// host sees it, once per environment rather than once per agent.
	ValidateEnvironment(ctx context.Context, desired *Environment) []diag.Diagnostic
	// PlanEnvironment decides what to do with the environment's object.
	// desired is nil when nothing should remain: on destroy, or when the
	// environment has no agents left. Most providers start from
	// DefaultEnvironmentPlan.
	PlanEnvironment(ctx context.Context, desired *Environment, prior *state.EnvironmentEntry) (*EnvironmentPlan, error)
	// ApplyEnvironment creates or updates the object. It may return a
	// result together with an error when the object exists but isn't
	// usable yet, such as a self-hosted environment whose worker can't
	// start; the engine records the result and reports the error.
	ApplyEnvironment(ctx context.Context, p *EnvironmentPlan) (*Applied, error)
	// ReadEnvironment looks up the object, for refresh and drift.
	ReadEnvironment(ctx context.Context, e *state.EnvironmentEntry) (*Observed, error)
	// DestroyEnvironment removes the object.
	DestroyEnvironment(ctx context.Context, e *state.EnvironmentEntry) error
}

// Environment is an environment as its host sees it: its spec and the
// agents it receives.
type Environment struct {
	Name string
	Spec *v1alpha1.EnvironmentSpec
	// Dir is the folder of the environment's file, which relative paths
	// in Spec start from.
	Dir    string
	Agents []*resolve.Agent
}

// ID returns the environment's logical ID.
func (e *Environment) ID() string { return v1alpha1.KindEnvironment + "/" + e.Name }

// EnvironmentPlan is what will happen to an environment's object.
type EnvironmentPlan struct {
	ID     string
	Action Action
	// Desired is nil for a delete.
	Desired *Environment
	// Prior is nil for a create.
	Prior *state.EnvironmentEntry
	// Digest and Config describe the desired object in the provider's own
	// terms. The engine records them; Config is what later plans diff.
	Digest string
	Config map[string]any
	// Changes, Reason and Notes are as in Plan.
	Changes []Change
	Reason  string
	Notes   []string
}

// DefaultEnvironmentPlan compares a desired config with what was applied:
// create, update when the digest differs, delete when nothing is desired,
// otherwise nothing.
func DefaultEnvironmentPlan(id string, desired *Environment, digest string, config map[string]any, prior *state.EnvironmentEntry) *EnvironmentPlan {
	p := &EnvironmentPlan{ID: id, Desired: desired, Prior: prior, Digest: digest, Config: config}
	switch {
	case desired == nil && prior == nil:
		p.Action = NoOp
	case desired == nil:
		p.Action = Delete
	case prior == nil:
		p.Action = Create
	case prior.Digest != digest:
		p.Action = Update
		p.Changes = DiffConfig(prior.Config, config)
	default:
		p.Action = NoOp
	}
	return p
}
