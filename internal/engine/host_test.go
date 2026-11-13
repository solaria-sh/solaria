package engine

import (
	"context"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/solaria-sh/solaria/internal/diag"
	"github.com/solaria-sh/solaria/internal/digest"
	"github.com/solaria-sh/solaria/internal/provider"
	"github.com/solaria-sh/solaria/internal/state"
)

// hosted is a fake provider that also keeps an object for each
// Environment, whose config is the Environment's apt packages and how many
// agents it's for.
type hosted struct {
	*fake
	envs     map[string]int // host objects by ID, with their revision
	failNext error
}

func (h *hosted) ValidateEnvironment(_ context.Context, e *provider.Environment) []diag.Diagnostic {
	return []diag.Diagnostic{
		diag.Warnf("", diag.Pos{}, "target: once"),
		diag.Warnf("", diag.Pos{}, "%s: once each", e.ID()),
	}
}

func (h *hosted) PlanEnvironment(_ context.Context, d *provider.Environment, p *state.EnvironmentEntry) (*provider.EnvironmentPlan, error) {
	cfg := map[string]any{"agents": 0}
	if d != nil {
		cfg["agents"] = len(d.Agents)
		if sb := d.Sandbox; sb != nil && sb.Packages != nil {
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
	id := "env_" + p.Desired.Name
	if p.Prior != nil {
		id = p.Prior.Binding.HostID // updated in place
	}
	h.envs[id]++
	applied := &provider.Applied{Binding: state.Binding{HostID: id}, RemoteHash: h.hash(id)}
	err := h.failNext
	h.failNext = nil
	return applied, err
}

func (h *hosted) ReadEnvironment(_ context.Context, e *state.EnvironmentEntry) (*provider.Observed, error) {
	if _, ok := h.envs[e.Binding.HostID]; !ok {
		return &provider.Observed{}, nil
	}
	return &provider.Observed{Exists: true, RemoteHash: h.hash(e.Binding.HostID)}, nil
}

func (h *hosted) DestroyEnvironment(_ context.Context, e *state.EnvironmentEntry) error {
	*h.calls = append(*h.calls, h.name+" destroy "+e.ID)
	delete(h.envs, e.Binding.HostID)
	return nil
}

func (h *hosted) hash(id string) string { return strings.Repeat("r", h.envs[id]) }

func newHostedHarness(t *testing.T) (*harness, *hosted) {
	caps := provider.Capabilities{Fallback: true, Approvals: true, MCPTransports: []string{"stdio", "streamable-http"}, Destroy: provider.DestroyArchive}
	h := newHarness(t, caps)
	hp := &hosted{fake: h.fakes["limited"], envs: map[string]int{}}
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

func environmentYAML(name, spec string) string {
	return "apiVersion: solaria.sh/v1alpha1\nkind: Environment\nmetadata: {name: " + name + "}\nspec: " + spec + "\n"
}

// ids lists a plan's Environment steps as "action ID".
func ids(p *Plan) string {
	var out []string
	for _, s := range p.Environments {
		out = append(out, string(s.Action)+" "+s.ID)
	}
	return strings.Join(out, ", ")
}

func TestHostObjectLifecycle(t *testing.T) {
	h, hp := newHostedHarness(t)
	ctx := context.Background()
	e := h.hostedEngine(hp, project("anthropic:claude-haiku-5-5", ""))

	// Without spec.environments, the agent gets the built-in sandbox.
	p := plan(t, e, PlanOptions{Refresh: true})
	if got := ids(p); got != "create Environment/default" {
		t.Fatalf("environment steps = %s", got)
	}
	got := p.Diagnostics.String()
	if strings.Count(got, "target: once") != 1 || strings.Count(got, "Environment/default: once each") != 1 || !strings.Contains(got, "env.yaml") {
		t.Errorf("environment diagnostics = %s; want each once, on the target's file", got)
	}
	if c, _, _, _ := p.Counts(); c != 2 {
		t.Errorf("plan creates %d objects, want the environment and the agent", c)
	}
	if err := e.Apply(ctx, p); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(h.calls, ", "); got != "limited apply Environment/default, limited apply Agent/a" {
		t.Errorf("calls = %s; the environment must come before its agents", got)
	}
	st, err := h.backend.Read("dev")
	if err != nil {
		t.Fatal(err)
	}
	if e := st.Environments["Environment/default"]; e == nil || e.Binding.HostID != "env_default" || e.Config["agents"] != float64(1) {
		t.Fatalf("state environments = %+v", st.Environments)
	}
	if p := plan(t, e, PlanOptions{Refresh: true}); p.HasChanges() {
		t.Errorf("unchanged environment plans %s", ids(p))
	}

	// Moving the agent into an Environment creates its object and deletes
	// the built-in one, after the agent.
	files := map[string]string{
		"project/env.yaml":  envYAML("limited", "  environments: [{ref: base}]\n"),
		"project/base.yaml": environmentYAML("base", "{packages: {apt: [jq]}}"),
	}
	e = h.hostedEngine(hp, files)
	p = plan(t, e, PlanOptions{Refresh: true})
	if got := ids(p); got != "create Environment/base, delete Environment/default" {
		t.Fatalf("environment steps = %s", got)
	}
	if r := p.Environments[1].Reason; r != "this target gives it no agents" {
		t.Errorf("delete reason = %q", r)
	}
	var b strings.Builder
	if err := Render(&b, p); err != nil {
		t.Fatal(err)
	}
	if r := b.String(); !strings.Contains(r, "  + Environment/base\n\n  - Environment/default (this target gives it no agents)\n      host: env_default\n") {
		t.Errorf("render:\n%s", b.String())
	}
	h.calls = nil
	if err := e.Apply(ctx, p); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(h.calls, ", "); got != "limited apply Environment/base, limited destroy Environment/default" {
		t.Errorf("calls = %s", got)
	}

	// A sandbox change updates the Environment's object only.
	files["project/base.yaml"] = environmentYAML("base", "{packages: {apt: [jq, git]}}")
	e = h.hostedEngine(hp, files)
	p = plan(t, e, PlanOptions{Refresh: true})
	if got := ids(p); got != "update Environment/base" || len(p.Environments[0].Changes) != 1 || p.Environments[0].Changes[0].Path != "apt" {
		t.Fatalf("environment steps = %s %+v", got, p.Environments)
	}
	if err := e.Apply(ctx, p); err != nil {
		t.Fatal(err)
	}

	// Drift: the host object changed outside Solaria.
	hp.envs["env_base"]++
	if p := plan(t, e, PlanOptions{Refresh: true}); p.Environments[0].Action != provider.Update || !strings.Contains(p.Environments[0].Reason, "changed outside Solaria") {
		t.Errorf("drift plans %s (%s)", p.Environments[0].Action, p.Environments[0].Reason)
	}

	// Destroy deletes the agent first and the environment object last.
	h.calls = nil
	p = plan(t, e, PlanOptions{Refresh: true, Destroy: true})
	if err := e.Apply(ctx, p); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(h.calls, ", "); got != "limited destroy Agent/a, limited destroy Environment/base" {
		t.Errorf("destroy calls = %s", got)
	}
	if st, _ := h.backend.Read("dev"); len(st.Environments) != 0 || len(st.Agents) != 0 {
		t.Errorf("state after destroy: environments %+v, agents %v", st.Environments, st.Agents)
	}
}

func TestHostObjectPerEnvironment(t *testing.T) {
	h, hp := newHostedHarness(t)
	files := project("anthropic:claude-haiku-5-5", "  environments:\n    - ref: base\n    - ref: data\n      agents: {selector: {matchLabels: {sandbox: data}}}\n")
	files["project/base.yaml"] = environmentYAML("base", "{}")
	files["project/data.yaml"] = environmentYAML("data", "{packages: {apt: [duckdb]}}")
	agentDoc, _, _ := strings.Cut(strings.Replace(agentYAML, "MODEL", "anthropic:claude-haiku-5-5", 1), "---")
	files["project/b.yaml"] = strings.Replace(agentDoc, "metadata: {name: a}", "metadata: {name: b, labels: {sandbox: data}}", 1)
	e := h.hostedEngine(hp, files)
	p := plan(t, e, PlanOptions{})
	if got := ids(p); got != "create Environment/base, create Environment/data" {
		t.Fatalf("environment steps = %s", got)
	}
	for _, s := range p.Environments {
		if n := len(s.Desired.Agents); n != 1 {
			t.Errorf("%s is for %d agents, want 1", s.ID, n)
		}
	}
	if a := p.Environments[1].Desired.Agents[0]; a.Name != "b" || a.Environment != "data" || a.Sandbox.Packages.APT[0] != "duckdb" {
		t.Errorf("Environment/data agent = %s in %s", a.Name, a.Environment)
	}
}

func TestHostObjectRecordedOnPartialApply(t *testing.T) {
	h, hp := newHostedHarness(t)
	hp.failNext = errors.New("worker can't start yet")
	e := h.hostedEngine(hp, project("anthropic:claude-haiku-5-5", ""))
	err := e.Apply(context.Background(), plan(t, e, PlanOptions{}))
	if err == nil || !strings.Contains(err.Error(), "create Environment/default: worker can't start yet") {
		t.Fatalf("Apply: err = %v", err)
	}
	st, rerr := h.backend.Read("dev")
	if rerr != nil {
		t.Fatal(rerr)
	}
	if st.Environments["Environment/default"] == nil || len(st.Agents) != 0 {
		t.Errorf("state: environments %+v, agents %v; the environment object exists and must be recorded, and its agents wait", st.Environments, st.Agents)
	}
}

func TestHostObjectGoneIsCreatedAgain(t *testing.T) {
	h, hp := newHostedHarness(t)
	e := h.hostedEngine(hp, project("anthropic:claude-haiku-5-5", ""))
	if err := e.Apply(context.Background(), plan(t, e, PlanOptions{})); err != nil {
		t.Fatal(err)
	}
	clear(hp.envs)
	p := plan(t, e, PlanOptions{Refresh: true})
	if p.Environments[0].Action != provider.Create || !strings.Contains(p.Environments[0].Reason, "deleted outside Solaria") {
		t.Errorf("environment step = %s (%s)", p.Environments[0].Action, p.Environments[0].Reason)
	}
}

// State written before ADR 0020 kept one host object, under host. It
// becomes the target's default Environment's, updated in place.
func TestHostObjectFromOldState(t *testing.T) {
	h, hp := newHostedHarness(t)
	hp.envs["env_1"] = 1
	old := `{"format": "solaria.sh/state/v1", "lineage": "l", "serial": 1, "environment": "dev", "agents": {},
	  "host": {"id": "Environment/dev", "provider": "limited", "placement": "here", "digest": "sha256:old",
	    "binding": {"hostId": "env_1"}, "remoteHash": "r", "source": {}, "appliedAt": "2026-11-01T00:00:00Z"}}`
	if err := os.MkdirAll(filepath.Join(h.dir, "state"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.dir, "state", "dev.json"), []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	files := project("anthropic:claude-haiku-5-5", "  environments: [{ref: base}]\n")
	files["project/base.yaml"] = environmentYAML("base", "{}")
	e := h.hostedEngine(hp, files)
	p := plan(t, e, PlanOptions{Refresh: true})
	if got := ids(p); got != "update Environment/base" {
		t.Fatalf("environment steps = %s; the old host object must be updated in place", got)
	}
	if err := e.Apply(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	st, err := h.backend.Read("dev")
	if err != nil {
		t.Fatal(err)
	}
	if got := st.Environments["Environment/base"]; len(st.Environments) != 1 || got == nil || got.Binding.HostID != "env_1" {
		t.Errorf("state environments = %v", slices.Collect(maps.Keys(st.Environments)))
	}
}
