package engine

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/solaria-sh/solaria/internal/diag"
	"github.com/solaria-sh/solaria/internal/digest"
	"github.com/solaria-sh/solaria/internal/provider"
	"github.com/solaria-sh/solaria/internal/state"
)

// hosted is a fake provider that also keeps an environment object, whose
// config is the environment's sandbox packages.
type hosted struct {
	*fake
	env      *state.Binding // the host object, nil when absent
	revision int
	failNext error
}

func (h *hosted) ValidateEnvironment(context.Context, *provider.Environment) []diag.Diagnostic {
	return []diag.Diagnostic{diag.Warnf("", diag.Pos{}, "Environment/dev: once")}
}

func (h *hosted) PlanEnvironment(_ context.Context, d *provider.Environment, p *state.EnvironmentEntry) (*provider.EnvironmentPlan, error) {
	cfg := map[string]any{"agents": 0}
	if d != nil {
		cfg["agents"] = len(d.Agents)
		if sb := d.Spec.Sandbox; sb != nil && sb.Packages != nil {
			cfg["apt"] = sb.Packages.APT
		}
	}
	dg, err := digest.Of(cfg)
	if err != nil {
		return nil, err
	}
	return provider.DefaultEnvironmentPlan("", d, dg, cfg, p), nil
}

func (h *hosted) ApplyEnvironment(_ context.Context, p *provider.EnvironmentPlan) (*provider.Applied, error) {
	*h.calls = append(*h.calls, h.name+" apply "+p.ID)
	h.revision++
	h.env = &state.Binding{HostID: "env_1"}
	applied := &provider.Applied{Binding: *h.env, RemoteHash: h.hash()}
	err := h.failNext
	h.failNext = nil
	return applied, err
}

func (h *hosted) ReadEnvironment(context.Context, *state.EnvironmentEntry) (*provider.Observed, error) {
	if h.env == nil {
		return &provider.Observed{}, nil
	}
	return &provider.Observed{Exists: true, RemoteHash: h.hash()}, nil
}

func (h *hosted) DestroyEnvironment(_ context.Context, e *state.EnvironmentEntry) error {
	*h.calls = append(*h.calls, h.name+" destroy "+e.ID)
	h.env = nil
	return nil
}

func (h *hosted) hash() string { return strings.Repeat("r", h.revision) }

func newHostedHarness(t *testing.T) (*harness, *hosted) {
	caps := provider.Capabilities{Fallback: true, Approvals: true, MCPTransports: []string{"stdio", "streamable-http"}, Destroy: provider.DestroyArchive}
	h := newHarness(t, caps)
	hp := &hosted{fake: h.fakes["limited"]}
	return h, hp
}

func (h *harness) hostedEngine(hp *hosted, files map[string]string) *Engine {
	e := h.engine(files)
	e.NewProvider = func(name string, _ provider.Options) (provider.Provider, error) {
		if name == "limited" {
			return hp, nil
		}
		if f, ok := h.fakes[name]; ok {
			return f, nil
		}
		return nil, errors.New("no such provider")
	}
	return e
}

func plan(t *testing.T, e *Engine, opts PlanOptions) *Plan {
	t.Helper()
	p, err := e.Plan(context.Background(), "dev", opts)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestHostObjectLifecycle(t *testing.T) {
	h, hp := newHostedHarness(t)
	ctx := context.Background()
	e := h.hostedEngine(hp, project("anthropic:claude-haiku-5-5", ""))

	p := plan(t, e, PlanOptions{Refresh: true})
	if p.Host == nil || p.Host.Action != provider.Create || p.Host.ID != "Environment/dev" {
		t.Fatalf("host step = %+v", p.Host)
	}
	if got := p.Diagnostics.String(); strings.Count(got, "Environment/dev: once") != 1 || !strings.Contains(got, "env.yaml") {
		t.Errorf("environment diagnostics = %s; want one, on the environment's file", got)
	}
	if c, _, _, _ := p.Counts(); c != 2 {
		t.Errorf("plan creates %d objects, want the environment and the agent", c)
	}
	if err := e.Apply(ctx, p); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(h.calls, ", "); got != "limited apply Environment/dev, limited apply Agent/a" {
		t.Errorf("calls = %s; the environment must come before its agents", got)
	}
	st, err := h.backend.Read("dev")
	if err != nil {
		t.Fatal(err)
	}
	if st.Host == nil || st.Host.Binding.HostID != "env_1" || st.Host.Config["agents"] != float64(1) {
		t.Fatalf("state host = %+v", st.Host)
	}

	if p := plan(t, e, PlanOptions{Refresh: true}); p.HasChanges() {
		t.Errorf("unchanged environment plans %s", p.Host.Action)
	}

	// A sandbox change updates the environment object only.
	e = h.hostedEngine(hp, map[string]string{"project/env.yaml": envYAML("limited", "  sandbox: {packages: {apt: [jq]}}\n")})
	p = plan(t, e, PlanOptions{Refresh: true})
	if p.Host.Action != provider.Update || len(p.Host.Changes) != 1 || p.Host.Changes[0].Path != "apt" {
		t.Fatalf("host step = %s %+v", p.Host.Action, p.Host.Changes)
	}
	var b strings.Builder
	if err := Render(&b, p); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), "  ~ Environment/dev\n      ~ apt: (none) -> [\"jq\"]\n") {
		t.Errorf("render:\n%s", b.String())
	}
	if err := e.Apply(ctx, p); err != nil {
		t.Fatal(err)
	}

	// Drift: the host object changed outside Solaria.
	hp.revision++
	if p := plan(t, e, PlanOptions{Refresh: true}); p.Host.Action != provider.Update || !strings.Contains(p.Host.Reason, "changed outside Solaria") {
		t.Errorf("drift plans %s (%s)", p.Host.Action, p.Host.Reason)
	}

	// Destroy deletes the agent first and the environment object last.
	h.calls = nil
	p = plan(t, e, PlanOptions{Refresh: true, Destroy: true})
	if err := e.Apply(ctx, p); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(h.calls, ", "); got != "limited destroy Agent/a, limited destroy Environment/dev" {
		t.Errorf("destroy calls = %s", got)
	}
	if st, _ := h.backend.Read("dev"); st.Host != nil || len(st.Agents) != 0 {
		t.Errorf("state after destroy: host %+v, agents %v", st.Host, st.Agents)
	}
}

func TestHostObjectRecordedOnPartialApply(t *testing.T) {
	h, hp := newHostedHarness(t)
	hp.failNext = errors.New("worker can't start yet")
	e := h.hostedEngine(hp, project("anthropic:claude-haiku-5-5", ""))
	err := e.Apply(context.Background(), plan(t, e, PlanOptions{}))
	if err == nil || !strings.Contains(err.Error(), "create Environment/dev: worker can't start yet") {
		t.Fatalf("Apply: err = %v", err)
	}
	st, rerr := h.backend.Read("dev")
	if rerr != nil {
		t.Fatal(rerr)
	}
	if st.Host == nil || len(st.Agents) != 0 {
		t.Errorf("state: host %+v, agents %v; the environment object exists and must be recorded, and its agents wait", st.Host, st.Agents)
	}
}

func TestHostObjectGoneIsCreatedAgain(t *testing.T) {
	h, hp := newHostedHarness(t)
	e := h.hostedEngine(hp, project("anthropic:claude-haiku-5-5", ""))
	if err := e.Apply(context.Background(), plan(t, e, PlanOptions{})); err != nil {
		t.Fatal(err)
	}
	hp.env = nil
	p := plan(t, e, PlanOptions{Refresh: true})
	if p.Host.Action != provider.Create || !strings.Contains(p.Host.Reason, "deleted outside Solaria") {
		t.Errorf("host step = %s (%s)", p.Host.Action, p.Host.Reason)
	}
}
