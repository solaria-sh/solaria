package engine

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/solaria-sh/solaria/internal/config"
	"github.com/solaria-sh/solaria/internal/diag"
	"github.com/solaria-sh/solaria/internal/provider"
	"github.com/solaria-sh/solaria/internal/resolve"
	"github.com/solaria-sh/solaria/internal/state"
	"github.com/solaria-sh/solaria/internal/validate"
)

// fake is an in-memory provider whose capabilities tests choose.
type fake struct {
	name    string
	caps    provider.Capabilities
	objects map[string]int // host objects by logical ID, with their version
	calls   *[]string
}

func (f *fake) Name() string                              { return f.name }
func (f *fake) Version() string                           { return "0.0.1" }
func (f *fake) Placements() []string                      { return []string{"here"} }
func (f *fake) Capabilities(string) provider.Capabilities { return f.caps }
func (f *fake) Validate(context.Context, *resolve.Agent) []diag.Diagnostic {
	return nil
}

func (f *fake) Plan(_ context.Context, d *resolve.Agent, p *state.Entry) (*provider.Plan, error) {
	return provider.DefaultPlan(d, p), nil
}

func (f *fake) Apply(_ context.Context, p *provider.Plan) (*provider.Applied, error) {
	*f.calls = append(*f.calls, f.name+" apply "+p.ID)
	f.objects[p.ID]++
	return &provider.Applied{Binding: state.Binding{HostID: f.name + ":" + p.ID}, RemoteHash: "h"}, nil
}

func (f *fake) Read(_ context.Context, e *state.Entry) (*provider.Observed, error) {
	_, ok := f.objects[e.ID]
	return &provider.Observed{Exists: ok, RemoteHash: "h"}, nil
}

func (f *fake) Destroy(_ context.Context, e *state.Entry) error {
	*f.calls = append(*f.calls, f.name+" destroy "+e.ID)
	delete(f.objects, e.ID)
	return nil
}

type harness struct {
	t       *testing.T
	dir     string
	fakes   map[string]*fake
	calls   []string
	backend *state.Local
}

func newHarness(t *testing.T, caps provider.Capabilities) *harness {
	h := &harness{t: t, dir: t.TempDir()}
	h.backend = &state.Local{Dir: filepath.Join(h.dir, "state")}
	h.fakes = map[string]*fake{
		"limited": {name: "limited", caps: caps, objects: map[string]int{}, calls: &h.calls},
		"other":   {name: "other", caps: provider.Capabilities{Destroy: provider.DestroyDelete}, objects: map[string]int{}, calls: &h.calls},
	}
	return h
}

func (h *harness) engine(files map[string]string) *Engine {
	h.t.Helper()
	for name, content := range files {
		p := filepath.Join(h.dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			h.t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			h.t.Fatal(err)
		}
	}
	set, diags := config.Load([]string{filepath.Join(h.dir, "project")})
	diags = append(diags, validate.Validate(set)...)
	if diags.HasErrors() {
		h.t.Fatalf("invalid fixture: %v", diags)
	}
	return &Engine{
		Set:     set,
		Backend: h.backend,
		NewProvider: func(name string, _ provider.Options) (provider.Provider, error) {
			if f, ok := h.fakes[name]; ok {
				return f, nil
			}
			return nil, errors.New("no such provider")
		},
		Now: func() time.Time { return time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC) },
	}
}

const agentYAML = `apiVersion: solaria.sh/v1alpha1
kind: Agent
metadata: {name: a}
spec:
  instructions: [role.md]
  model:
    preferred: MODEL
    fallback: [openai:gpt-6-astra]
  tools:
    builtin: [shell]
    mcp: [{ref: local-only}, {ref: remote}]
  permissions: {ask: [shell]}
---
apiVersion: solaria.sh/v1alpha1
kind: McpServer
metadata: {name: local-only}
spec:
  packages: [{registryType: npm, identifier: x, version: "1.0.0", transport: {type: stdio}}]
---
apiVersion: solaria.sh/v1alpha1
kind: McpServer
metadata: {name: remote}
spec:
  remotes: [{type: streamable-http, url: "https://example.com/mcp"}]
`

func envYAML(provider, extra string) string {
	return "apiVersion: solaria.sh/v1alpha1\nkind: Target\nmetadata: {name: dev}\nspec:\n  provider: " + provider + "\n  placement: here\n" + extra
}

func project(model, envExtra string) map[string]string {
	return map[string]string{
		"project/agent.yaml": strings.Replace(agentYAML, "MODEL", model, 1),
		"project/env.yaml":   envYAML("limited", envExtra),
		"project/role.md":    "Role.\n",
		"project/Dockerfile": "FROM scratch\n",
	}
}

func TestCapabilityChecks(t *testing.T) {
	caps := provider.Capabilities{
		ModelProviders: []string{"anthropic"},
		MCPTransports:  []string{provider.TransportStreamableHTTP},
		Destroy:        provider.DestroyArchive,
	}
	h := newHarness(t, caps)
	e := h.engine(project("openai:gpt-6-astra", "  sandbox: {image: ./Dockerfile}\n"))

	_, err := e.Plan(context.Background(), "dev", PlanOptions{})
	var failed *FailedError
	if !errors.As(err, &failed) {
		t.Fatalf("Plan: err = %v, want a FailedError", err)
	}
	got := failed.Diagnostics.String()
	for _, want := range []string{
		"error: Agent/a on limited/here: model openai:gpt-6-astra isn't available; this host runs anthropic models only",
		`error: Agent/a on limited/here: McpServer "local-only" offers stdio, and this host connects over streamable-http only`,
		"error: Agent/a on limited/here: sandbox.image isn't supported here",
		"warning: Agent/a on limited/here: model.fallback is ignored; this host has no model fallback",
		"warning: Agent/a on limited/here: this host can't ask for approval in unattended runs, so shell will be denied",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("diagnostics lack %q; got:\n%s", want, got)
		}
	}
	if strings.Contains(got, `"remote"`) {
		t.Errorf("remote MCP server reported although the host supports it:\n%s", got)
	}
	if len(h.calls) > 0 {
		t.Errorf("host was called during a failed plan: %v", h.calls)
	}
}

func TestCapabilityWarningsDoNotStopThePlan(t *testing.T) {
	caps := provider.Capabilities{
		ModelProviders: []string{"anthropic"},
		Fallback:       true,
		MCPTransports:  []string{provider.TransportStdio, provider.TransportStreamableHTTP},
		Destroy:        provider.DestroyDelete,
	}
	e := newHarness(t, caps).engine(project("anthropic:claude-haiku-5-5", ""))
	plan, err := e.Plan(context.Background(), "dev", PlanOptions{})
	if err != nil {
		t.Fatal(err)
	}
	got := plan.Diagnostics.String()
	if !strings.Contains(got, "fallback model openai:gpt-6-astra is skipped") || !strings.Contains(got, "shell will be denied") {
		t.Errorf("warnings = %s", got)
	}
	if c, _, _, _ := plan.Counts(); c != 1 {
		t.Errorf("plan creates %d agents, want 1", c)
	}
}

// TestOutputSchemaCapability checks ADR 0019: plan fails for an agent with
// an output schema on a host that can't enforce it, before any host call,
// and passes on one that can.
func TestOutputSchemaCapability(t *testing.T) {
	files := project("anthropic:claude-haiku-5-5", "")
	files["project/agent.yaml"] = strings.Replace(files["project/agent.yaml"],
		"  permissions: {ask: [shell]}\n",
		"  permissions: {ask: [shell]}\n  output:\n    schema: {type: object, properties: {verdict: {type: string}}}\n", 1)
	caps := provider.Capabilities{
		ModelProviders: []string{"anthropic"},
		Fallback:       true,
		Approvals:      true,
		MCPTransports:  []string{provider.TransportStdio, provider.TransportStreamableHTTP},
		Destroy:        provider.DestroyDelete,
	}

	h := newHarness(t, caps)
	_, err := h.engine(files).Plan(context.Background(), "dev", PlanOptions{})
	var failed *FailedError
	if !errors.As(err, &failed) {
		t.Fatalf("Plan: err = %v, want a FailedError", err)
	}
	want := "error: Agent/a on limited/here: spec.output.schema can't be enforced here; this host doesn't support output schemas (ADR 0019)"
	if got := failed.Diagnostics.String(); !strings.Contains(got, want) {
		t.Errorf("diagnostics lack %q; got:\n%s", want, got)
	}
	if len(h.calls) > 0 {
		t.Errorf("host was called during a failed plan: %v", h.calls)
	}

	caps.OutputSchema = true
	if _, err := newHarness(t, caps).engine(files).Plan(context.Background(), "dev", PlanOptions{}); err != nil {
		t.Errorf("Plan with the capability: %v", err)
	}
}

func TestProviderChangeReplaces(t *testing.T) {
	open := provider.Capabilities{Fallback: true, Approvals: true, MCPTransports: []string{"stdio", "streamable-http"}, Destroy: provider.DestroyDelete}
	h := newHarness(t, open)
	h.fakes["other"].caps = open
	ctx := context.Background()

	e := h.engine(project("anthropic:claude-haiku-5-5", ""))
	plan, err := e.Plan(ctx, "dev", PlanOptions{Refresh: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Apply(ctx, plan); err != nil {
		t.Fatal(err)
	}

	e = h.engine(map[string]string{"project/env.yaml": envYAML("other", "")})
	plan, err = e.Plan(ctx, "dev", PlanOptions{Refresh: true})
	if err != nil {
		t.Fatal(err)
	}
	step := plan.Steps[0]
	if step.Action != provider.Replace || step.Reason != "moves from limited/here to other/here" {
		t.Fatalf("step = %s (%s), want a replace moving providers", step.Action, step.Reason)
	}
	if err := e.Apply(ctx, plan); err != nil {
		t.Fatal(err)
	}
	want := []string{"limited apply Agent/a", "limited destroy Agent/a", "other apply Agent/a"}
	if strings.Join(h.calls, ", ") != strings.Join(want, ", ") {
		t.Errorf("calls = %v, want %v", h.calls, want)
	}

	st, err := h.backend.Read("dev")
	if err != nil {
		t.Fatal(err)
	}
	entry := st.Agents["Agent/a"]
	if st.Serial != 2 || entry.Provider != "other" || len(entry.History) != 1 || entry.History[0].Binding.HostID != "limited:Agent/a" {
		t.Errorf("state after replace: serial %d, entry %+v", st.Serial, entry)
	}
	if entry.SpecDigest != entry.History[0].SpecDigest {
		t.Error("moving providers changed the specDigest; the digest must not depend on the host")
	}
}

func TestDestroyWithoutEnvironmentInConfig(t *testing.T) {
	open := provider.Capabilities{Fallback: true, Approvals: true, MCPTransports: []string{"stdio", "streamable-http"}, Destroy: provider.DestroyDelete}
	h := newHarness(t, open)
	ctx := context.Background()
	e := h.engine(project("anthropic:claude-haiku-5-5", ""))
	plan, err := e.Plan(ctx, "dev", PlanOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Apply(ctx, plan); err != nil {
		t.Fatal(err)
	}

	if err := os.Remove(filepath.Join(h.dir, "project", "env.yaml")); err != nil {
		t.Fatal(err)
	}
	e = h.engine(nil)
	if _, err := e.Plan(ctx, "dev", PlanOptions{}); err == nil {
		t.Error("plan of an environment no longer in the configuration succeeded")
	}
	plan, err = e.Plan(ctx, "dev", PlanOptions{Destroy: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Apply(ctx, plan); err != nil {
		t.Fatal(err)
	}
	if len(h.fakes["limited"].objects) != 0 {
		t.Errorf("objects left after destroy: %v", h.fakes["limited"].objects)
	}
}

func TestLockBlocksSecondWriter(t *testing.T) {
	e := newHarness(t, provider.Capabilities{}).engine(project("anthropic:claude-haiku-5-5", ""))
	unlock, err := e.Lock("dev", "apply")
	if err != nil {
		t.Fatal(err)
	}
	var locked *state.LockedError
	if _, err := e.Lock("dev", "apply"); !errors.As(err, &locked) {
		t.Errorf("second Lock: err = %v, want LockedError", err)
	}
	if err := unlock(); err != nil {
		t.Fatal(err)
	}
}

func TestRenderArchive(t *testing.T) {
	caps := provider.Capabilities{Fallback: true, Approvals: true, MCPTransports: []string{"stdio", "streamable-http"}, Destroy: provider.DestroyArchive}
	h := newHarness(t, caps)
	ctx := context.Background()
	e := h.engine(project("anthropic:claude-haiku-5-5", ""))
	plan, err := e.Plan(ctx, "dev", PlanOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Apply(ctx, plan); err != nil {
		t.Fatal(err)
	}
	plan, err = e.Plan(ctx, "dev", PlanOptions{Destroy: true})
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	if err := Render(&b, plan); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), "  -/archive Agent/a\n") || !strings.Contains(b.String(), "1 to delete or archive.") {
		t.Errorf("an archiving host's destroy plan should say so:\n%s", b.String())
	}
}
