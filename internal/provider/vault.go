package provider

import (
	"context"

	"github.com/solaria-sh/solaria/internal/api/v1alpha1"
	"github.com/solaria-sh/solaria/internal/diag"
	"github.com/solaria-sh/solaria/internal/resolve"
	"github.com/solaria-sh/solaria/internal/state"
)

// VaultHost is implemented by providers whose host keeps credentials in
// vaults, which sessions attach when they start (ADR 0016). A Managed
// Agents vault is one. Providers whose launcher passes credentials into
// the run don't implement it: there, a Vault only scopes what the launcher
// passes.
//
// The engine keeps one host vault per Vault that the target's agents
// reference. It creates or updates them before the agents and deletes them
// after.
type VaultHost interface {
	// ValidateVault reports what the host can't hold, before any host call.
	ValidateVault(ctx context.Context, desired *Vault) []diag.Diagnostic
	// PlanVault decides what to do with a host vault. desired is never nil;
	// the engine plans deletes itself. Most providers start from
	// DefaultVaultPlan.
	PlanVault(ctx context.Context, desired *Vault, prior *state.VaultEntry) (*VaultPlan, error)
	// ApplyVault creates or updates the host vault, reading the values it
	// sends from the target's secret source.
	ApplyVault(ctx context.Context, p *VaultPlan) (*Applied, error)
	// ReadVault looks up the host vault, for refresh and drift.
	ReadVault(ctx context.Context, e *state.VaultEntry) (*Observed, error)
	// DestroyVault removes the host vault and its credentials.
	DestroyVault(ctx context.Context, e *state.VaultEntry) error
}

// Vault is a Vault as its host sees it in one target.
type Vault struct {
	Name string
	// Credentials are sorted by name.
	Credentials []VaultCredential
	// Agents are the logical IDs of the target's agents that
	// reference the vault, sorted.
	Agents []string
	// Env is the target the vault is for. Its secret source holds
	// the credentials' values.
	Env       *Environment
	Placement string
}

// ID returns the vault's logical ID.
func (v *Vault) ID() string { return v1alpha1.KindVault + "/" + v.Name }

// VaultCredential is one credential of a Vault.
type VaultCredential struct {
	resolve.Credential
	// Server is the McpServer an mcpServer credential is for.
	Server *v1alpha1.McpServerSpec
}

// VaultPlan is what will happen to a host vault.
type VaultPlan struct {
	ID     string
	Action Action
	// Desired is nil for a delete.
	Desired *Vault
	// Prior is nil for a create.
	Prior *state.VaultEntry
	// Digest and Config describe the desired vault in the provider's own
	// terms; Config names credentials and secrets, never values.
	Digest string
	Config map[string]any
	// Rotate sends every credential's value again, for apply
	// --rotate-secrets. Otherwise apply sends a value only when its
	// credential is created or changes.
	Rotate bool
	// Changes, Reason and Notes are as in Plan.
	Changes []Change
	Reason  string
	Notes   []string
}

// DefaultVaultPlan compares a desired config with what was applied:
// create, update when the digest differs, otherwise nothing.
func DefaultVaultPlan(desired *Vault, digest string, config map[string]any, prior *state.VaultEntry) *VaultPlan {
	p := &VaultPlan{ID: desired.ID(), Desired: desired, Prior: prior, Digest: digest, Config: config}
	switch {
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
