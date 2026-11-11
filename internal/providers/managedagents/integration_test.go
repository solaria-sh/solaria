package managedagents

import (
	"context"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go/v2"

	"github.com/solaria-sh/solaria/internal/engine"
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

// TestManagedAgentsVaultIntegration applies the fixture with a Vault to the
// real API (ADR 0016): it creates a host vault with a bearer token and a
// variable, checks that a plan right after apply is clean, rotates the
// values, changes the variable, and destroys everything. The values are
// placeholders: the host stores them and never calls the docs server.
func TestManagedAgentsVaultIntegration(t *testing.T) {
	if os.Getenv("SOLARIA_IT_MANAGED_AGENTS") != "1" {
		t.Skip("set SOLARIA_IT_MANAGED_AGENTS=1, with ANTHROPIC_API_KEY, to create real Managed Agents objects")
	}
	if os.Getenv("ANTHROPIC_API_KEY") == "" {
		t.Fatal("SOLARIA_IT_MANAGED_AGENTS needs ANTHROPIC_API_KEY")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	t.Setenv("DOCS_TOKEN", "Bearer solaria-it-placeholder-docs")
	t.Setenv("GH_PAT", "solaria-it-placeholder-gh")

	r := newRig(t)
	r.client = anthropic.NewClient()
	vaultProject(r)
	r.edit("solaria.yaml", strings.Replace(readFile(t, filepath.Join(r.dir, "solaria.yaml")), "namePrefix: solaria-test-", "namePrefix: solaria-it-", 1))
	apply := func(opts engine.PlanOptions) {
		t.Helper()
		e := r.engine()
		pl, err := e.Plan(ctx, "ma-cloud", opts)
		if err != nil {
			t.Fatal(err)
		}
		if err := e.Apply(ctx, pl); err != nil {
			t.Fatal(err)
		}
	}
	clean := func(when string) {
		t.Helper()
		p, err := r.engine().Plan(ctx, "ma-cloud", engine.PlanOptions{Refresh: true})
		if err != nil {
			t.Fatal(err)
		}
		if p.HasChanges() {
			var b strings.Builder
			_ = engine.Render(&b, p)
			t.Errorf("a plan right after %s has changes:\n%s", when, b.String())
		}
	}
	// credentials returns the vault's credentials by display name, as
	// type and target.
	credentials := func(vaultID string) map[string]string {
		t.Helper()
		out := map[string]string{}
		iter := r.client.Beta.Vaults.Credentials.ListAutoPaging(ctx, vaultID, anthropic.BetaVaultCredentialListParams{})
		for iter.Next() {
			c := iter.Current()
			out[c.DisplayName] = c.Auth.Type + " " + c.Auth.MCPServerURL + c.Auth.SecretName
		}
		if err := iter.Err(); err != nil {
			t.Fatal(err)
		}
		return out
	}
	destroyed := false
	t.Cleanup(func() {
		if !destroyed {
			apply(engine.PlanOptions{Refresh: true, Destroy: true})
		}
	})

	apply(engine.PlanOptions{Refresh: true})
	st, err := r.state.Read("ma-cloud")
	if err != nil {
		t.Fatal(err)
	}
	vaultID := st.Vaults["Vault/tools"].Binding.HostID
	v, err := r.client.Beta.Vaults.Get(ctx, vaultID, anthropic.BetaVaultGetParams{})
	if err != nil {
		t.Fatal(err)
	}
	if v.DisplayName != "solaria-it-ma-cloud/tools" || v.Metadata[metaLogicalID] != "Vault/tools" {
		t.Errorf("created vault: name %q, metadata %v", v.DisplayName, v.Metadata)
	}
	want := map[string]string{
		"docs": "static_bearer https://docs.example.com/mcp",
		"gh":   "environment_variable GH_TOKEN",
	}
	if got := credentials(vaultID); !maps.Equal(got, want) {
		t.Errorf("credentials after create: %v, want %v", got, want)
	}
	clean("create")

	t.Setenv("GH_PAT", "solaria-it-placeholder-gh-2")
	apply(engine.PlanOptions{Refresh: true, RotateSecrets: true})
	clean("rotate")

	r.edit("solaria.yaml", strings.Replace(readFile(t, filepath.Join(r.dir, "solaria.yaml")), "for: {env: GH_TOKEN}", "for: {env: GITHUB_TOKEN}", 1))
	apply(engine.PlanOptions{Refresh: true})
	want["gh"] = "environment_variable GITHUB_TOKEN"
	if got := credentials(vaultID); !maps.Equal(got, want) {
		t.Errorf("credentials after changing the variable: %v, want %v", got, want)
	}
	clean("changing the variable")

	apply(engine.PlanOptions{Refresh: true, Destroy: true})
	destroyed = true
	if v, err := r.client.Beta.Vaults.Get(ctx, vaultID, anthropic.BetaVaultGetParams{}); err == nil && v.ArchivedAt.IsZero() {
		t.Error("destroy left the vault")
	}
}
