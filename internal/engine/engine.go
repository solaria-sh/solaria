// Package engine plans and applies a target: it resolves agents,
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
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/solaria-sh/solaria/internal/api/v1alpha1"
	"github.com/solaria-sh/solaria/internal/config"
	"github.com/solaria-sh/solaria/internal/diag"
	"github.com/solaria-sh/solaria/internal/lockfile"
	"github.com/solaria-sh/solaria/internal/provider"
	"github.com/solaria-sh/solaria/internal/resolve"
	"github.com/solaria-sh/solaria/internal/state"
)

// Engine plans and applies targets of one project.
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
	// LockFile pins the digests targets may run (ADR 0013); nil means
	// none are pinned. LockFilePath names it in diagnostics.
	LockFile     *lockfile.File
	LockFilePath string
}

// PlanOptions change what a plan does.
type PlanOptions struct {
	// Refresh asks the host about every entry first, to detect drift.
	Refresh bool
	// Destroy plans the removal of everything in the state.
	Destroy bool
	// RotateSecrets sends every host vault's credential values again,
	// although state can't tell whether they changed (ADR 0016).
	RotateSecrets bool
}

// Plan is what an apply would do to one target.
type Plan struct {
	Target    string
	Provider  string
	Placement string
	// Options are the options the plan was made with.
	Options PlanOptions
	// Capabilities of the target's provider and placement.
	Capabilities provider.Capabilities
	// Steps for every agent in the configuration or the state, sorted by
	// ID. No-op steps are included.
	Steps []*provider.Plan
	// Environments are the steps for the host objects of the Environments
	// the target's agents use, sorted by ID, for providers whose host keeps
	// them (ADR 0012, ADR 0020). No-op steps are included.
	Environments []*provider.EnvironmentPlan
	// Vaults are the steps for host vaults, sorted by ID, for providers
	// whose host keeps credentials in vaults (ADR 0016). No-op steps are
	// included.
	Vaults []*provider.VaultPlan
	// Diagnostics are warnings found while planning.
	Diagnostics diag.List

	state     *state.State
	providers map[string]provider.Provider
}

// Counts returns how many steps create, update, replace and delete,
// counting the Environments' host objects and vaults with the agents.
func (p *Plan) Counts() (create, update, replace, del int) {
	actions := make([]provider.Action, 0, len(p.Steps)+len(p.Vaults)+len(p.Environments))
	for _, s := range p.Steps {
		actions = append(actions, s.Action)
	}
	for _, v := range p.Vaults {
		actions = append(actions, v.Action)
	}
	for _, h := range p.Environments {
		actions = append(actions, h.Action)
	}
	for _, a := range actions {
		switch a {
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

// Lock takes the target's lock for a write operation. Call the
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
	plan := &Plan{Target: env, Options: opts, state: st, providers: map[string]provider.Provider{}}

	envRes := e.Set.Lookup(v1alpha1.KindTarget, env)
	switch {
	case envRes != nil:
		spec := envRes.Target().Spec
		plan.Provider, plan.Placement = spec.Provider, spec.Placement
	case opts.Destroy && len(st.Agents) > 0:
		// The target left the configuration, but its state still
		// says what to clean up and with which provider.
		first := st.Agents[st.IDs()[0]]
		plan.Provider, plan.Placement = first.Provider, first.Placement
	case opts.Destroy && len(st.Environments) > 0:
		first := st.Environments[st.EnvironmentIDs()[0]]
		plan.Provider, plan.Placement = first.Provider, first.Placement
	case opts.Destroy && len(st.Vaults) > 0:
		first := st.Vaults[st.VaultIDs()[0]]
		plan.Provider, plan.Placement = first.Provider, first.Placement
	default:
		return nil, fmt.Errorf("no Target named %q", env)
	}

	prov, err := e.provider(plan, plan.Provider)
	if err != nil {
		return nil, err
	}
	if !slices.Contains(prov.Placements(), plan.Placement) {
		return nil, fmt.Errorf("provider %s has no placement %q; it has %v", plan.Provider, plan.Placement, prov.Placements())
	}
	plan.Capabilities = prov.Capabilities(plan.Placement)

	if envRes != nil {
		st.AdoptLegacyHost(resolve.DefaultEnvironment(&envRes.Target().Spec))
	} else {
		st.AdoptLegacyHost(v1alpha1.DefaultEnvironment)
	}

	var desired []resolve.Agent
	var wants []*provider.Environment // the host's view, for providers with environment objects
	var vaults []*provider.Vault      // host vaults, for providers that keep them
	if !opts.Destroy {
		desired, err = resolve.Target(e.Set, env)
		if err != nil {
			return nil, err
		}
		// A locked target takes only the digests in the lock. This
		// needs only the configuration, so it runs before any host call.
		diags := e.LockFile.Check(e.LockFilePath, env, desired)
		if diags.HasErrors() {
			return nil, &FailedError{Diagnostics: diags}
		}
		if host, ok := prov.(provider.EnvironmentHost); ok {
			wants = e.desiredEnvironments(desired, envRes)
			for _, want := range wants {
				for _, d := range host.ValidateEnvironment(ctx, want) {
					if d.File == "" {
						d.File = envRes.File
					}
					// Problems with the target, such as its settings,
					// come back once per Environment.
					if !slices.Contains(diags, d) {
						diags = append(diags, d)
					}
				}
			}
		}
		if vh, ok := prov.(provider.VaultHost); ok {
			spec := envRes.Target().Spec
			view := &provider.Environment{Target: env, Dir: filepath.Dir(envRes.File), Spec: &spec}
			vaults = e.desiredVaults(desired, view, plan.Placement)
			for _, v := range vaults {
				for _, d := range vh.ValidateVault(ctx, v) {
					if d.File == "" {
						d.File = e.Set.Lookup(v1alpha1.KindVault, v.Name).File
					}
					diags = append(diags, d)
				}
			}
		}
		for i := range desired {
			a := &desired[i]
			file := e.Set.Lookup(v1alpha1.KindAgent, a.Name).File
			envFile := ""
			if er := e.Set.Lookup(v1alpha1.KindEnvironment, a.Environment); er != nil {
				envFile = er.File
			}
			diags = append(diags, checkCapabilities(a, plan.Capabilities, file, envFile)...)
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
			step.Reason = "no longer selected by this target"
		default:
			step.Reason = "no longer in the configuration"
		}
		if obs := observed[id]; obs != nil && !obs.Exists {
			step.Reason = joinReason(step.Reason, "already gone from the host")
		}
		plan.Steps = append(plan.Steps, step)
	}
	slices.SortFunc(plan.Steps, func(a, b *provider.Plan) int { return cmp.Compare(a.ID, b.ID) })

	if err := e.planEnvironments(ctx, plan, prov, wants, opts); err != nil {
		return nil, err
	}
	if err := e.planVaults(ctx, plan, prov, vaults, opts); err != nil {
		return nil, err
	}
	return plan, nil
}

// desiredVaults collects the Vaults the target's agents reference,
// with the McpServers their credentials are for. A credential for a git
// host authenticates clones, which the host's vaults have no part in, so
// it's left out, and so is a vault that holds nothing else.
func (e *Engine) desiredVaults(agents []resolve.Agent, env *provider.Environment, placement string) []*provider.Vault {
	byName := map[string]*provider.Vault{}
	for i := range agents {
		a := &agents[i]
		for name, rv := range a.Definition.Vaults {
			if !slices.ContainsFunc(rv.Credentials, func(c resolve.Credential) bool { return c.GitHost == "" }) {
				continue
			}
			v := byName[name]
			if v == nil {
				v = &provider.Vault{Name: name, Env: env, Placement: placement}
				for _, c := range rv.Credentials { // sorted by resolve
					if c.GitHost != "" {
						continue
					}
					vc := provider.VaultCredential{Credential: c}
					if r := e.Set.Lookup(v1alpha1.KindMcpServer, c.MCPServer); c.MCPServer != "" && r != nil {
						spec := r.McpServer().Spec
						vc.Server = &spec
					}
					v.Credentials = append(v.Credentials, vc)
				}
				byName[name] = v
			}
			v.Agents = append(v.Agents, a.ID()) // agents come sorted
		}
	}
	out := make([]*provider.Vault, 0, len(byName))
	for _, name := range slices.Sorted(maps.Keys(byName)) {
		out = append(out, byName[name])
	}
	return out
}

// planVaults plans the host vaults (ADR 0016): one for each Vault the
// target's agents reference, while its provider keeps vaults.
func (e *Engine) planVaults(ctx context.Context, plan *Plan, prov provider.Provider, desired []*provider.Vault, opts PlanOptions) error {
	st := plan.state
	vh, _ := prov.(provider.VaultHost)
	seen := map[string]bool{}
	for _, v := range desired {
		id := v.ID()
		seen[id] = true
		prior := st.Vaults[id]
		if prior != nil && (prior.Provider != plan.Provider || prior.Placement != plan.Placement) {
			step, err := vh.PlanVault(ctx, v, nil)
			if err != nil {
				return fmt.Errorf("plan %s: %w", id, err)
			}
			step.ID, step.Action, step.Prior = id, provider.Replace, prior
			step.Reason = fmt.Sprintf("moves from %s/%s to %s/%s", prior.Provider, prior.Placement, plan.Provider, plan.Placement)
			plan.Vaults = append(plan.Vaults, step)
			continue
		}
		var obs *provider.Observed
		if opts.Refresh && prior != nil {
			var err error
			if obs, err = vh.ReadVault(ctx, prior); err != nil {
				return fmt.Errorf("refresh %s: %w", id, err)
			}
		}
		gone := obs != nil && !obs.Exists
		planPrior := prior
		if gone {
			planPrior = nil // the provider must create it again
		}
		step, err := vh.PlanVault(ctx, v, planPrior)
		if err != nil {
			return fmt.Errorf("plan %s: %w", id, err)
		}
		step.ID, step.Prior = id, prior
		switch {
		case gone:
			step.Reason = joinReason(step.Reason, "deleted outside Solaria; it will be created again")
		case obs != nil && obs.RemoteHash != prior.RemoteHash:
			if step.Action == provider.NoOp {
				step.Action = provider.Update
			}
			step.Reason = joinReason(step.Reason, "changed outside Solaria since the last apply")
		}
		if opts.RotateSecrets && step.Action != provider.Create {
			step.Rotate = true
			if step.Action == provider.NoOp {
				step.Action = provider.Update
			}
			step.Reason = joinReason(step.Reason, "sends every credential's value again")
		}
		plan.Vaults = append(plan.Vaults, step)
	}
	for _, id := range st.VaultIDs() {
		if seen[id] {
			continue
		}
		step := &provider.VaultPlan{ID: id, Action: provider.Delete, Prior: st.Vaults[id]}
		switch {
		case opts.Destroy:
		case e.Set.Lookup(v1alpha1.KindVault, name(id)) != nil:
			step.Reason = "no agent in this target references it"
		default:
			step.Reason = "no longer in the configuration"
		}
		plan.Vaults = append(plan.Vaults, step)
	}
	slices.SortFunc(plan.Vaults, func(a, b *provider.VaultPlan) int { return cmp.Compare(a.ID, b.ID) })
	return nil
}

// desiredEnvironments groups a target's agents by the Environment it
// gives them, as hosts with environment objects see each one, sorted by
// name.
func (e *Engine) desiredEnvironments(agents []resolve.Agent, target *config.Resource) []*provider.Environment {
	spec := target.Target().Spec
	byName := map[string]*provider.Environment{}
	for i := range agents {
		a := &agents[i]
		want := byName[a.Environment]
		if want == nil {
			want = &provider.Environment{
				Name: a.Environment, Target: target.Name, Spec: &spec, Dir: target.Dir(),
				Sandbox: a.Sandbox, SandboxDir: a.SandboxDir,
			}
			byName[a.Environment] = want
		}
		want.Agents = append(want.Agents, a) // agents come sorted
	}
	out := make([]*provider.Environment, 0, len(byName))
	for _, n := range slices.Sorted(maps.Keys(byName)) {
		out = append(out, byName[n])
	}
	return out
}

// planEnvironments plans the Environments' host objects (ADR 0012, ADR
// 0020): one for each Environment the target gives at least one agent,
// while its provider keeps them.
func (e *Engine) planEnvironments(ctx context.Context, plan *Plan, prov provider.Provider, desired []*provider.Environment, opts PlanOptions) error {
	st := plan.state
	host, _ := prov.(provider.EnvironmentHost)
	seen := map[string]bool{}
	for _, want := range desired {
		id := want.ID()
		seen[id] = true
		step, err := e.planEnvironment(ctx, plan, host, want, st.Environments[id], opts.Refresh)
		if err != nil {
			return fmt.Errorf("plan %s: %w", id, err)
		}
		plan.Environments = append(plan.Environments, step)
	}
	for _, id := range st.EnvironmentIDs() {
		if seen[id] {
			continue
		}
		prior := st.Environments[id]
		step := &provider.EnvironmentPlan{ID: id, Action: provider.Delete, Prior: prior}
		switch {
		case opts.Destroy:
		case prior.Provider != plan.Provider || prior.Placement != plan.Placement:
			step.Reason = fmt.Sprintf("moves from %s/%s to %s/%s", prior.Provider, prior.Placement, plan.Provider, plan.Placement)
		case e.Set.Lookup(v1alpha1.KindEnvironment, name(id)) != nil || name(id) == v1alpha1.DefaultEnvironment:
			step.Reason = "this target gives it no agents"
		default:
			step.Reason = "no longer in the configuration"
		}
		if opts.Refresh && host != nil && prior.Provider == plan.Provider {
			obs, err := host.ReadEnvironment(ctx, prior)
			if err != nil {
				return fmt.Errorf("refresh %s: %w", id, err)
			}
			if !obs.Exists {
				step.Reason = joinReason(step.Reason, "already gone from the host")
			}
		}
		plan.Environments = append(plan.Environments, step)
	}
	slices.SortFunc(plan.Environments, func(a, b *provider.EnvironmentPlan) int { return cmp.Compare(a.ID, b.ID) })
	return nil
}

// planEnvironment plans one Environment's host object.
func (e *Engine) planEnvironment(ctx context.Context, plan *Plan, host provider.EnvironmentHost, want *provider.Environment, prior *state.EnvironmentEntry, refresh bool) (*provider.EnvironmentPlan, error) {
	id := want.ID()
	if prior != nil && (prior.Provider != plan.Provider || prior.Placement != plan.Placement) {
		step, err := host.PlanEnvironment(ctx, want, nil)
		if err != nil {
			return nil, err
		}
		step.ID, step.Action, step.Prior = id, provider.Replace, prior
		step.Reason = fmt.Sprintf("moves from %s/%s to %s/%s", prior.Provider, prior.Placement, plan.Provider, plan.Placement)
		return step, nil
	}
	var obs *provider.Observed
	if refresh && prior != nil {
		var err error
		if obs, err = host.ReadEnvironment(ctx, prior); err != nil {
			return nil, fmt.Errorf("refresh: %w", err)
		}
	}
	gone := obs != nil && !obs.Exists
	planPrior := prior
	if gone {
		planPrior = nil // the provider must create it again
	}
	step, err := host.PlanEnvironment(ctx, want, planPrior)
	if err != nil {
		return nil, err
	}
	step.ID, step.Prior = id, prior
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
	step.Observed = obs
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

// Apply carries out a plan made under the target's lock. It deletes
// agents first, then creates or updates the Environments' host objects and
// the vaults, applies the other agent steps by ID, and deletes vaults and
// then Environments' host objects last. It writes the state once at the end, including
// after a failure, so completed steps are recorded.
func (e *Engine) Apply(ctx context.Context, plan *Plan) error {
	if !plan.HasChanges() {
		return nil
	}
	var deletes, others []*provider.Plan
	for _, step := range plan.Steps {
		if step.Action == provider.Delete {
			deletes = append(deletes, step)
		} else {
			others = append(others, step)
		}
	}
	var order []func() error
	agentSteps := func(steps []*provider.Plan) {
		for _, step := range steps {
			order = append(order, func() error {
				if err := e.applyStep(ctx, plan, step); err != nil {
					return fmt.Errorf("%s %s: %w", step.Action, step.ID, err)
				}
				return nil
			})
		}
	}
	environmentSteps := func(deletes bool) {
		for _, step := range plan.Environments {
			if (step.Action == provider.Delete) != deletes {
				continue
			}
			order = append(order, func() error {
				if err := e.applyEnvironment(ctx, plan, step); err != nil {
					return fmt.Errorf("%s %s: %w", step.Action, step.ID, err)
				}
				return nil
			})
		}
	}
	vaultSteps := func(deletes bool) {
		for _, step := range plan.Vaults {
			if (step.Action == provider.Delete) != deletes {
				continue
			}
			order = append(order, func() error {
				if err := e.applyVault(ctx, plan, step); err != nil {
					return fmt.Errorf("%s %s: %w", step.Action, step.ID, err)
				}
				return nil
			})
		}
	}
	agentSteps(deletes)
	environmentSteps(false)
	vaultSteps(false)
	agentSteps(others)
	vaultSteps(true)
	environmentSteps(true)
	var applyErr error
	for _, f := range order {
		if applyErr = f(); applyErr != nil {
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

// applyEnvironment carries out an Environment host object's step.
func (e *Engine) applyEnvironment(ctx context.Context, plan *Plan, step *provider.EnvironmentPlan) error {
	st := plan.state
	switch step.Action {
	case provider.NoOp:
		return nil
	case provider.Delete, provider.Replace:
		old, err := e.environmentHost(plan, step.Prior.Provider)
		if err != nil {
			return err
		}
		if err := old.DestroyEnvironment(ctx, step.Prior); err != nil {
			return err
		}
		delete(st.Environments, step.ID)
		if step.Action == provider.Delete {
			return nil
		}
	}
	host, err := e.environmentHost(plan, plan.Provider)
	if err != nil {
		return err
	}
	applied, err := host.ApplyEnvironment(ctx, step)
	if applied != nil {
		p, _ := e.provider(plan, plan.Provider) // created by environmentHost
		if st.Environments == nil {
			st.Environments = map[string]*state.EnvironmentEntry{}
		}
		st.Environments[step.ID] = &state.EnvironmentEntry{
			ID:              step.ID,
			Provider:        plan.Provider,
			ProviderVersion: p.Version(),
			Placement:       plan.Placement,
			Digest:          step.Digest,
			Config:          step.Config,
			Binding:         applied.Binding,
			RemoteHash:      applied.RemoteHash,
			Source:          e.Source,
			AppliedAt:       e.now(),
			AppliedBy:       e.Who,
		}
	}
	return err
}

// applyVault carries out a host vault's step.
func (e *Engine) applyVault(ctx context.Context, plan *Plan, step *provider.VaultPlan) error {
	st := plan.state
	switch step.Action {
	case provider.NoOp:
		return nil
	case provider.Delete, provider.Replace:
		old, err := e.vaultHost(plan, step.Prior.Provider)
		if err != nil {
			return err
		}
		if err := old.DestroyVault(ctx, step.Prior); err != nil {
			return err
		}
		delete(st.Vaults, step.ID)
		if step.Action == provider.Delete {
			return nil
		}
	}
	vh, err := e.vaultHost(plan, plan.Provider)
	if err != nil {
		return err
	}
	applied, err := vh.ApplyVault(ctx, step)
	if applied != nil {
		p, _ := e.provider(plan, plan.Provider) // created by vaultHost
		entry := &state.VaultEntry{
			ID:              step.ID,
			Provider:        plan.Provider,
			ProviderVersion: p.Version(),
			Placement:       plan.Placement,
			Digest:          step.Digest,
			Config:          step.Config,
			Binding:         applied.Binding,
			RemoteHash:      applied.RemoteHash,
			Source:          e.Source,
			AppliedAt:       e.now(),
			AppliedBy:       e.Who,
		}
		if err != nil {
			// Part of the vault was applied. No digest matches an empty
			// one, so the next plan updates it.
			entry.Digest = ""
		}
		if st.Vaults == nil {
			st.Vaults = map[string]*state.VaultEntry{}
		}
		st.Vaults[step.ID] = entry
	}
	return err
}

// vaultHost returns a provider that keeps vaults.
func (e *Engine) vaultHost(plan *Plan, name string) (provider.VaultHost, error) {
	p, err := e.provider(plan, name)
	if err != nil {
		return nil, err
	}
	vh, ok := p.(provider.VaultHost)
	if !ok {
		return nil, fmt.Errorf("provider %s keeps no vaults", name)
	}
	return vh, nil
}

// environmentHost returns a provider that keeps environment objects.
func (e *Engine) environmentHost(plan *Plan, name string) (provider.EnvironmentHost, error) {
	p, err := e.provider(plan, name)
	if err != nil {
		return nil, err
	}
	host, ok := p.(provider.EnvironmentHost)
	if !ok {
		return nil, fmt.Errorf("provider %s keeps no environment object", name)
	}
	return host, nil
}

// provider returns the plan's instance of a provider, creating it once.
func (e *Engine) provider(plan *Plan, name string) (provider.Provider, error) {
	if p, ok := plan.providers[name]; ok {
		return p, nil
	}
	p, err := e.NewProvider(name, provider.Options{Target: plan.Target, WorkDir: e.WorkDir, Log: e.Log})
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

// Entry returns the state entry for an ID as it stands in the plan's state,
// which after Apply is the newly written one.
func (p *Plan) Entry(id string) *state.Entry { return p.state.Agents[id] }

// EnvironmentEntry returns an Environment's host object as it stands in
// the plan's state, or nil.
func (p *Plan) EnvironmentEntry(id string) *state.EnvironmentEntry { return p.state.Environments[id] }

// VaultEntry returns a host vault as it stands in the plan's state, or nil.
func (p *Plan) VaultEntry(id string) *state.VaultEntry { return p.state.Vaults[id] }
