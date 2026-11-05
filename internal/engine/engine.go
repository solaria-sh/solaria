// Package engine plans and applies an environment: it resolves agents,
// checks them against their provider's capabilities, refreshes state from
// the host, compares the two, and drives the provider to close the gap
// (ADR 0006, ADR 0009).
package engine

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/renantarouco/solaria/internal/api/v1alpha1"
	"github.com/renantarouco/solaria/internal/config"
	"github.com/renantarouco/solaria/internal/diag"
	"github.com/renantarouco/solaria/internal/provider"
	"github.com/renantarouco/solaria/internal/resolve"
	"github.com/renantarouco/solaria/internal/state"
)

// Engine plans and applies environments of one project.
type Engine struct {
	Set     *config.Set
	Backend state.Backend
	// NewProvider creates providers by name.
	NewProvider func(name string, opts provider.Options) (provider.Provider, error)
	// WorkDir is the project directory, passed to providers.
	WorkDir string
	// Log receives providers' progress output. Nil means discard.
	Log io.Writer
	// Source and Who are recorded with every applied entry.
	Source state.Source
	Who    string
	// Now defaults to time.Now.
	Now func() time.Time
}

// PlanOptions change what a plan does.
type PlanOptions struct {
	// Refresh asks the host about every entry first, to detect drift.
	Refresh bool
	// Destroy plans the removal of everything in the state.
	Destroy bool
}

// Plan is what an apply would do to one environment.
type Plan struct {
	Environment string
	Provider    string
	Placement   string
	// Capabilities of the environment's provider and placement.
	Capabilities provider.Capabilities
	// Steps for every agent in the configuration or the state, sorted by
	// ID. No-op steps are included.
	Steps []*provider.Plan
	// Diagnostics are warnings found while planning.
	Diagnostics diag.List

	state     *state.State
	providers map[string]provider.Provider
}

// Counts returns how many steps create, update, replace and delete.
func (p *Plan) Counts() (create, update, replace, del int) {
	for _, s := range p.Steps {
		switch s.Action {
		case provider.Create:
			create++
		case provider.Update:
			update++
		case provider.Replace:
			replace++
		case provider.Delete:
			del++
		}
	}
	return create, update, replace, del
}

// HasChanges reports whether applying the plan would change anything.
func (p *Plan) HasChanges() bool {
	c, u, r, d := p.Counts()
	return c+u+r+d > 0
}

// Serial is the state serial the plan was made from (0 if no state yet).
func (p *Plan) Serial() int { return p.state.Serial }

// FailedError is a plan that found errors.
type FailedError struct{ Diagnostics diag.List }

func (e *FailedError) Error() string {
	n, _ := e.Diagnostics.Count()
	if n == 1 {
		return "plan failed with 1 error"
	}
	return fmt.Sprintf("plan failed with %d errors", n)
}

// Lock takes the environment's lock for a write operation. Call the
// returned function to release it.
func (e *Engine) Lock(env, operation string) (unlock func() error, err error) {
	nonce, err := e.Backend.Lock(env, state.LockInfo{Operation: operation, Who: e.Who, Created: e.now()})
	if err != nil {
		return nil, err
	}
	return func() error { return e.Backend.Unlock(env, nonce) }, nil
}

func (e *Engine) now() time.Time {
	if e.Now != nil {
		return e.Now().UTC()
	}
	return time.Now().UTC()
}

// Plan works out what applying the configuration to env would do.
func (e *Engine) Plan(ctx context.Context, env string, opts PlanOptions) (*Plan, error) {
	st, err := e.Backend.Read(env)
	if err != nil {
		return nil, err
	}
	if st == nil {
		st = state.New(env)
	}
	plan := &Plan{Environment: env, state: st, providers: map[string]provider.Provider{}}

	envRes := e.Set.Lookup(v1alpha1.KindEnvironment, env)
	switch {
	case envRes != nil:
		spec := envRes.Environment().Spec
		plan.Provider, plan.Placement = spec.Provider, spec.Placement
	case opts.Destroy && len(st.Agents) > 0:
		// The environment left the configuration, but its state still
		// says what to clean up and with which provider.
		first := st.Agents[st.IDs()[0]]
		plan.Provider, plan.Placement = first.Provider, first.Placement
	default:
		return nil, fmt.Errorf("no Environment named %q", env)
	}

	prov, err := e.provider(plan, plan.Provider)
	if err != nil {
		return nil, err
	}
	if !slices.Contains(prov.Placements(), plan.Placement) {
		return nil, fmt.Errorf("provider %s has no placement %q; it has %v", plan.Provider, plan.Placement, prov.Placements())
	}
	plan.Capabilities = prov.Capabilities(plan.Placement)

	var desired []resolve.Agent
	if !opts.Destroy {
		desired, err = resolve.Environment(e.Set, env)
		if err != nil {
			return nil, err
		}
		var diags diag.List
		for i := range desired {
			a := &desired[i]
			file := e.Set.Lookup(v1alpha1.KindAgent, a.Name).File
			diags = append(diags, checkCapabilities(a, plan.Capabilities, file)...)
			for _, d := range prov.Validate(ctx, a) {
				if d.File == "" {
					d.File = file
				}
				diags = append(diags, d)
			}
		}
		diags.Sort()
		if diags.HasErrors() {
			return nil, &FailedError{Diagnostics: diags}
		}
		plan.Diagnostics = diags
	}

	observed := map[string]*provider.Observed{}
	if opts.Refresh {
		for _, id := range st.IDs() {
			entry := st.Agents[id]
			p, err := e.provider(plan, entry.Provider)
			if err != nil {
				return nil, fmt.Errorf("refresh %s: %w", id, err)
			}
			obs, err := p.Read(ctx, entry)
			if err != nil {
				return nil, fmt.Errorf("refresh %s: %w", id, err)
			}
			observed[id] = obs
		}
	}

	seen := map[string]bool{}
	for i := range desired {
		a := &desired[i]
		seen[a.ID()] = true
		step, err := e.planAgent(ctx, prov, a, st.Agents[a.ID()], observed[a.ID()])
		if err != nil {
			return nil, fmt.Errorf("plan %s: %w", a.ID(), err)
		}
		plan.Steps = append(plan.Steps, step)
	}
	for _, id := range st.IDs() {
		if seen[id] {
			continue
		}
		step := &provider.Plan{ID: id, Action: provider.Delete, Prior: st.Agents[id]}
		switch {
		case opts.Destroy:
		case e.Set.Lookup(v1alpha1.KindAgent, name(id)) != nil:
			step.Reason = "no longer selected by this environment"
		default:
			step.Reason = "no longer in the configuration"
		}
		if obs := observed[id]; obs != nil && !obs.Exists {
			step.Reason = joinReason(step.Reason, "already gone from the host")
		}
		plan.Steps = append(plan.Steps, step)
	}
	slices.SortFunc(plan.Steps, func(a, b *provider.Plan) int { return cmp.Compare(a.ID, b.ID) })
	return plan, nil
}

func (e *Engine) planAgent(ctx context.Context, prov provider.Provider, a *resolve.Agent, prior *state.Entry, obs *provider.Observed) (*provider.Plan, error) {
	if prior != nil && (prior.Provider != a.Provider || prior.Placement != a.Placement) {
		return &provider.Plan{
			ID: a.ID(), Action: provider.Replace, Desired: a, Prior: prior,
			Changes: provider.Diff(&prior.Definition, &a.Definition),
			Reason:  fmt.Sprintf("moves from %s/%s to %s/%s", prior.Provider, prior.Placement, a.Provider, a.Placement),
		}, nil
	}
	gone := prior != nil && obs != nil && !obs.Exists
	planPrior := prior
	if gone {
		planPrior = nil // the provider must create it again
	}
	step, err := prov.Plan(ctx, a, planPrior)
	if err != nil {
		return nil, err
	}
	step.Prior = prior // keep history even when recreating
	switch {
	case gone:
		step.Reason = joinReason(step.Reason, "deleted outside Solaria; it will be created again")
	case obs != nil && obs.RemoteHash != prior.RemoteHash:
		if step.Action == provider.NoOp {
			step.Action = provider.Update
		}
		step.Reason = joinReason(step.Reason, "changed outside Solaria since the last apply")
	}
	return step, nil
}

// Apply carries out a plan made under the environment's lock. It applies
// deletes first, then the other steps by ID, and writes the state once at
// the end, including after a failure, so completed steps are recorded.
func (e *Engine) Apply(ctx context.Context, plan *Plan) error {
	if !plan.HasChanges() {
		return nil
	}
	steps := slices.Clone(plan.Steps)
	slices.SortStableFunc(steps, func(a, b *provider.Plan) int {
		return cmp.Compare(boolInt(a.Action != provider.Delete), boolInt(b.Action != provider.Delete))
	})
	var applyErr error
	for _, step := range steps {
		if err := e.applyStep(ctx, plan, step); err != nil {
			applyErr = fmt.Errorf("%s %s: %w", step.Action, step.ID, err)
			break
		}
	}
	if err := e.Backend.Write(plan.state); err != nil {
		return errors.Join(applyErr, fmt.Errorf("write state: %w", err))
	}
	return applyErr
}

func (e *Engine) applyStep(ctx context.Context, plan *Plan, step *provider.Plan) error {
	st := plan.state
	switch step.Action {
	case provider.NoOp:
		return nil
	case provider.Delete:
		p, err := e.provider(plan, step.Prior.Provider)
		if err != nil {
			return err
		}
		if err := p.Destroy(ctx, step.Prior); err != nil {
			return err
		}
		delete(st.Agents, step.ID)
		return nil
	case provider.Replace:
		old, err := e.provider(plan, step.Prior.Provider)
		if err != nil {
			return err
		}
		if err := old.Destroy(ctx, step.Prior); err != nil {
			return fmt.Errorf("destroy the previous object: %w", err)
		}
		delete(st.Agents, step.ID)
	}

	p, err := e.provider(plan, step.Desired.Provider)
	if err != nil {
		return err
	}
	applied, err := p.Apply(ctx, step)
	if err != nil {
		return err
	}
	a := step.Desired
	entry := &state.Entry{
		ID:              a.ID(),
		Provider:        a.Provider,
		ProviderVersion: p.Version(),
		Placement:       a.Placement,
		SpecDigest:      a.SpecDigest,
		ResolvedDigest:  a.ResolvedDigest,
		Binding:         applied.Binding,
		RemoteHash:      applied.RemoteHash,
		Definition:      a.Definition,
		Source:          e.Source,
		AppliedAt:       e.now(),
		AppliedBy:       e.Who,
	}
	if prior := step.Prior; prior != nil {
		entry.History = append(slices.Clone(prior.History), prior.Revision())
	}
	st.Agents[entry.ID] = entry
	return nil
}

// provider returns the plan's instance of a provider, creating it once.
func (e *Engine) provider(plan *Plan, name string) (provider.Provider, error) {
	if p, ok := plan.providers[name]; ok {
		return p, nil
	}
	p, err := e.NewProvider(name, provider.Options{Environment: plan.Environment, WorkDir: e.WorkDir, Log: e.Log})
	if err != nil {
		return nil, err
	}
	plan.providers[name] = p
	return p, nil
}

func name(id string) string {
	_, n, _ := strings.Cut(id, "/")
	return n
}

func joinReason(a, b string) string {
	if a == "" {
		return b
	}
	return a + "; " + b
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// Entry returns the state entry for an ID as it stands in the plan's state,
// which after Apply is the newly written one.
func (p *Plan) Entry(id string) *state.Entry { return p.state.Agents[id] }
