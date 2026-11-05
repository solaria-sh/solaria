package managedagents

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go/v2"

	"github.com/renantarouco/solaria/internal/engine"
)

// TestManagedAgentsIntegration applies the fixture's cloud environment to
// the real Managed Agents API, under names starting with solaria-it-,
// updates it, and destroys it. It creates real objects, which destroy
// archives (agents) or deletes, so it only runs with
// SOLARIA_IT_MANAGED_AGENTS=1 and an ANTHROPIC_API_KEY.
func TestManagedAgentsIntegration(t *testing.T) {
	if os.Getenv("SOLARIA_IT_MANAGED_AGENTS") != "1" {
		t.Skip("set SOLARIA_IT_MANAGED_AGENTS=1, with ANTHROPIC_API_KEY, to create real Managed Agents objects")
	}
	if os.Getenv("ANTHROPIC_API_KEY") == "" {
		t.Fatal("SOLARIA_IT_MANAGED_AGENTS needs ANTHROPIC_API_KEY")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	r := newRig(t)
	r.client = anthropic.NewClient()
	r.edit("solaria.yaml", strings.Replace(readFile(t, filepath.Join(r.dir, "solaria.yaml")), "namePrefix: solaria-test-", "namePrefix: solaria-it-", 1))
	apply := func(opts engine.PlanOptions) *engine.Plan {
		t.Helper()
		e := r.engine()
		pl, err := e.Plan(ctx, "ma-cloud", opts)
		if err != nil {
			t.Fatal(err)
		}
		if err := e.Apply(ctx, pl); err != nil {
			t.Fatal(err)
		}
		return pl
	}
	destroyed := false
	t.Cleanup(func() {
		if !destroyed {
			apply(engine.PlanOptions{Refresh: true, Destroy: true})
		}
	})

	apply(engine.PlanOptions{Refresh: true})
	id := r.agentID()
	agent, err := r.client.Beta.Agents.Get(ctx, id, anthropic.BetaAgentGetParams{})
	if err != nil {
		t.Fatal(err)
	}
	if agent.Name != "solaria-it-reviewer" || agent.Metadata[metaLogicalID] != "Agent/reviewer" || agent.Version != 1 {
		t.Errorf("created agent: name %q, metadata %v, version %d", agent.Name, agent.Metadata, agent.Version)
	}
	if p, err := r.engine().Plan(ctx, "ma-cloud", engine.PlanOptions{Refresh: true}); err != nil {
		t.Fatal(err)
	} else if p.HasChanges() {
		t.Error("a second plan right after apply has changes")
	}

	r.edit("role.md", "You review pull requests for correctness and tests.\n")
	apply(engine.PlanOptions{Refresh: true})
	if agent, err = r.client.Beta.Agents.Get(ctx, id, anthropic.BetaAgentGetParams{}); err != nil {
		t.Fatal(err)
	}
	if agent.Version != 2 || !strings.Contains(agent.System, "and tests") {
		t.Errorf("updated agent: version %d, system %q", agent.Version, agent.System)
	}

	apply(engine.PlanOptions{Refresh: true, Destroy: true})
	destroyed = true
	if agent, err = r.client.Beta.Agents.Get(ctx, id, anthropic.BetaAgentGetParams{}); err != nil {
		t.Fatal(err)
	}
	if agent.ArchivedAt.IsZero() {
		t.Error("destroy left the agent unarchived")
	}
}
