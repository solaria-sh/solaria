package engine

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/solaria-sh/solaria/internal/diag"
	"github.com/solaria-sh/solaria/internal/provider"
	"github.com/solaria-sh/solaria/internal/resolve"
	"github.com/solaria-sh/solaria/internal/state"
)

var update = flag.Bool("update", false, "rewrite golden files")

func digestOf(c byte) string { return "sha256:" + strings.Repeat(string(c), 64) }

func entry(id string, c byte) *state.Entry {
	return &state.Entry{ID: id, Provider: "limited", Placement: "here", SpecDigest: digestOf(c), ResolvedDigest: digestOf(c), Binding: state.Binding{HostID: "obj_" + id}}
}

func agent(name string, spec, resolved byte) *resolve.Agent {
	return &resolve.Agent{Name: name, Provider: "limited", Placement: "here", SpecDigest: digestOf(spec), ResolvedDigest: digestOf(resolved)}
}

// everyStep is a plan with every kind of step: each agent action, the
// environment's host object, and host vaults.
func everyStep() *Plan {
	st := &state.State{Format: state.Format, Lineage: "7e1d2c3b-4a5f-4e6d-8c7b-6a5f4e3d2c1b", Serial: 4, Environment: "dev"}
	return &Plan{
		Environment: "dev", Provider: "limited", Placement: "here",
		Options: PlanOptions{Refresh: true, RotateSecrets: true},
		Steps: []*provider.Plan{
			{ID: "Agent/created", Action: provider.Create, Desired: agent("created", 'a', 'a')},
			{ID: "Agent/deleted", Action: provider.Delete, Prior: entry("Agent/deleted", 'b'), Reason: "no longer in the configuration"},
			{ID: "Agent/kept", Action: provider.NoOp, Desired: agent("kept", 'c', 'c'), Prior: entry("Agent/kept", 'c')},
			{
				ID: "Agent/moved", Action: provider.Replace, Desired: agent("moved", 'd', 'd'), Prior: entry("Agent/moved", 'd'),
				Reason: "moves from other/here to limited/here",
			},
			{
				ID: "Agent/updated", Action: provider.Update, Desired: agent("updated", 'e', 'f'), Prior: entry("Agent/updated", 'e'),
				Changes: []provider.Change{
					{Path: "instructions[0]", Old: "3 lines", New: "5 lines", Text: true},
					{Path: "model.preferred", Old: `"anthropic:claude-haiku-5-5"`, New: `"anthropic:claude-sonnet-5-5"`},
				},
				Reason: "changed outside Solaria since the last apply",
				Notes:  []string{"the host keeps the previous version"},
			},
		},
		Host: &provider.EnvironmentPlan{
			ID: "Environment/dev", Action: provider.Update, Digest: digestOf('1'),
			Prior:   &state.EnvironmentEntry{ID: "Environment/dev", Digest: digestOf('0')},
			Changes: []provider.Change{{Path: "packages.apt", Old: `["git"]`, New: `["git","jq"]`}},
		},
		Vaults: []*provider.VaultPlan{
			{ID: "Vault/new", Action: provider.Create, Digest: digestOf('2')},
			{
				ID: "Vault/tracker", Action: provider.Update, Digest: digestOf('3'), Rotate: true,
				Prior:  &state.VaultEntry{ID: "Vault/tracker", Digest: digestOf('3')},
				Reason: "sends every credential's value again",
			},
		},
		Diagnostics: diag.List{diag.Warnf("agents.yaml", diag.Pos{Line: 3, Column: 5}, "a warning")},
		state:       st,
	}
}

func TestDocumentGolden(t *testing.T) {
	e := &Engine{Source: state.Source{Commit: "0123456789abcdef0123456789abcdef01234567", Dirty: true}}
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetIndent("", "  ")
	if err := enc.Encode(e.Document(everyStep())); err != nil {
		t.Fatal(err)
	}
	golden := filepath.Join("testdata", "plan.golden.json")
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(golden, b.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(b.Bytes(), want) {
		t.Errorf("plan JSON differs from %s (run go test -update):\n%s", golden, b.String())
	}

	// It reads back as what it was.
	var back Document
	if err := json.Unmarshal(b.Bytes(), &back); err != nil {
		t.Fatal(err)
	}
	if d := back.Differences(e.Document(everyStep())); len(d) > 0 {
		t.Errorf("a document differs from itself after a round trip: %q", d)
	}
}

func TestDocumentDifferences(t *testing.T) {
	e := &Engine{}
	saved := e.Document(everyStep())

	tests := []struct {
		name   string
		change func(p *Plan)
		want   []string
	}{
		{"same", func(*Plan) {}, nil},
		{"serial", func(p *Plan) { p.state.Serial = 5 }, []string{"the state changed since the plan: serial 4 then, 5 now"}},
		{"lineage", func(p *Plan) { p.state.Lineage = "other" }, []string{"the state was replaced since the plan: lineage 7e1d2c3b-4a5f-4e6d-8c7b-6a5f4e3d2c1b then, other now"}},
		{"removed", func(p *Plan) { p.state.Serial = 0 }, []string{"the state was removed since the plan, which was made from serial 4"}},
		{"host action", func(p *Plan) { p.Host.Action = provider.NoOp }, []string{"Environment/dev: the plan says update, and planning again says no-op"}},
		{"host digest", func(p *Plan) { p.Host.Digest = digestOf('9') }, []string{"Environment/dev: the plan applies sha256:111111111111, and the configuration now gives sha256:999999999999"}},
		{"host gone", func(p *Plan) { p.Host = nil }, []string{"Environment/dev is in the plan, and planning again doesn't find it"}},
		{"vault rotate", func(p *Plan) { p.Vaults[1].Rotate = false }, []string{"Vault/tracker: the plan and planning again differ on sending credential values again"}},
		{"new vault", func(p *Plan) {
			p.Vaults = append(p.Vaults, &provider.VaultPlan{ID: "Vault/z", Action: provider.Create, Digest: digestOf('4')})
		}, []string{"Vault/z isn't in the plan, and planning again finds it"}},
		{"applied digest", func(p *Plan) { p.Steps[4].Prior = entry("Agent/updated", '7') }, []string{
			"Agent/updated: the applied agent was sha256:eeeeeeeeeeee (spec sha256:eeeeeeeeeeee) in the plan, and is sha256:777777777777 (spec sha256:777777777777) now",
		}},
		{"new agent", func(p *Plan) {
			p.Steps = append(p.Steps, &provider.Plan{ID: "Agent/zz", Action: provider.Create, Desired: agent("zz", 'a', 'a')})
		}, []string{"Agent/zz isn't in the plan, and planning again finds it"}},
		{"provider", func(p *Plan) { p.Provider = "other" }, []string{"the environment moved from limited/here to other/here"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := everyStep()
			tt.change(p)
			if got := saved.Differences(e.Document(p)); !slices.Equal(got, tt.want) {
				t.Errorf("Differences:\n got %q\nwant %q", got, tt.want)
			}
		})
	}
}
