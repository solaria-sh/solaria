package managedagents

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"regexp"
	"slices"
	"strings"

	"github.com/anthropics/anthropic-sdk-go/v2"
	"github.com/anthropics/anthropic-sdk-go/v2/packages/param"

	"github.com/solaria-sh/solaria/internal/diag"
	"github.com/solaria-sh/solaria/internal/digest"
	"github.com/solaria-sh/solaria/internal/provider"
	"github.com/solaria-sh/solaria/internal/secrets"
	"github.com/solaria-sh/solaria/internal/state"
)

// Vault binding outputs: credential.<name>.id holds each credential's host
// ID.
const outCredentialPrefix = "credential."

// Host credential types.
const (
	credStaticBearer = "static_bearer"
	credEnvVar       = "environment_variable"
)

// hostCredential is how a Vault credential is stored on the host: a bearer
// token for an MCP server's URL, or an environment variable the host
// substitutes into the sandbox's outbound requests. It names the secret
// that holds the value, never the value.
type hostCredential struct {
	Type         string `json:"type"`
	MCPServerURL string `json:"mcpServerUrl,omitempty"`
	Variable     string `json:"variable,omitempty"`
	SecretRef    string `json:"secretRef"`
}

// sameTarget reports whether two credentials differ only in their secret.
// The host can't change a credential's type, URL or variable; any of
// those needs a new credential.
func (c hostCredential) sameTarget(o hostCredential) bool {
	return c.Type == o.Type && c.MCPServerURL == o.MCPServerURL && c.Variable == o.Variable
}

// hostCredentials maps a Vault's credentials to the host's, with what the
// host can't hold. A credential for an MCP server becomes a static bearer
// token for the server's remote URL, which the host sends as
// "Authorization: Bearer <token>". Managed Agents runs no MCP packages, so a
// secret only a package uses isn't stored.
func hostCredentials(v *provider.Vault, where string) (map[string]hostCredential, diag.List) {
	var diags diag.List
	out := map[string]hostCredential{}
	for _, c := range v.Credentials {
		if c.Env != "" {
			out[c.Name] = hostCredential{Type: credEnvVar, Variable: c.Env, SecretRef: c.SecretRef}
			continue
		}
		if c.Server == nil {
			continue // validate reports the missing McpServer
		}
		url, r := remote(*c.Server)
		if r == nil {
			diags = append(diags, diag.Warnf("", diag.Pos{}, "%s: credential %q is for McpServer %q, which has no remote, and Managed Agents runs no MCP packages; it isn't stored", where, c.Name, c.MCPServer))
			continue
		}
		var header string
		for _, h := range r.Headers {
			if h.SecretRef == c.SecretRef {
				header = h.Name
				break
			}
		}
		switch {
		case header == "":
			diags = append(diags, diag.Warnf("", diag.Pos{}, "%s: credential %q: McpServer %q uses secret %s only in a package variable, and Managed Agents runs no MCP packages; it isn't stored", where, c.Name, c.MCPServer, c.SecretRef))
		case !strings.EqualFold(header, "Authorization"):
			diags = append(diags, diag.Errorf("", diag.Pos{}, "%s: credential %q: McpServer %q sends secret %s as header %s, and Managed Agents vaults can only fill an MCP server's Authorization bearer token", where, c.Name, c.MCPServer, c.SecretRef, header))
		default:
			out[c.Name] = hostCredential{Type: credStaticBearer, MCPServerURL: url, SecretRef: c.SecretRef}
		}
	}
	return out, diags
}

func vaultWhere(v *provider.Vault) string {
	return fmt.Sprintf("%s on %s/%s", v.ID(), Name, v.Placement)
}

// vaultName is the host vault's display name: one per Vault and
// target.
func vaultName(v *provider.Vault, s settings) string {
	return s.namePrefix + v.Env.Name + "/" + v.Name
}

// ValidateVault reports what a Managed Agents vault can't hold.
func (p *Provider) ValidateVault(_ context.Context, v *provider.Vault) []diag.Diagnostic {
	_, diags := hostCredentials(v, vaultWhere(v))
	return diags
}

// PlanVault compares the vault's credentials, by name, target and secret,
// with what was applied. Values never take part: apply --rotate-secrets
// sends them again.
func (p *Provider) PlanVault(_ context.Context, v *provider.Vault, prior *state.VaultEntry) (*provider.VaultPlan, error) {
	s, _ := readSettings(v.Env.Spec, v.Placement)
	creds, _ := hostCredentials(v, vaultWhere(v))
	cfg := map[string]any{"name": vaultName(v, s), "credentials": creds}
	dg, err := digest.Of(cfg)
	if err != nil {
		return nil, err
	}
	pl := provider.DefaultVaultPlan(v, dg, cfg, prior)
	if pl.Action == provider.Create {
		names := slices.Sorted(maps.Keys(creds))
		pl.Notes = append(pl.Notes, fmt.Sprintf("stores %s from %s", plural(len(names), "credential"), secrets.For(v.Env.Spec.Secrets, v.Env.Dir).Describe))
	}
	pl.Notes = append(pl.Notes, "sessions of "+strings.Join(v.Agents, ", ")+" attach it when they start")
	return pl, nil
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}

// ApplyVault creates the vault, or brings its credentials in line: it
// deletes those the Vault no longer has, creates new ones, recreates those
// whose target changed, and sends a value again when its secret changed or
// the plan rotates. Every value is read before the first host call, so a
// missing secret changes nothing.
func (p *Provider) ApplyVault(ctx context.Context, pl *provider.VaultPlan) (*provider.Applied, error) {
	v := pl.Desired
	s, _ := readSettings(v.Env.Spec, v.Placement)
	desired, _ := hostCredentials(v, vaultWhere(v))
	update := pl.Action == provider.Update

	var prior map[string]hostCredential
	priorOut := map[string]string{}
	if update {
		var err error
		if prior, err = priorCredentials(pl.Prior); err != nil {
			return nil, err
		}
		priorOut = pl.Prior.Binding.Outputs
	}

	// What to send: a new credential, one whose target changed, one whose
	// secret changed, or every one when rotating.
	type change struct {
		create, send bool
	}
	changes := map[string]change{}
	for name, c := range desired {
		old, had := prior[name]
		switch {
		case !had || !old.sameTarget(c) || priorOut[outCredentialPrefix+name+".id"] == "":
			changes[name] = change{create: true, send: true}
		case old.SecretRef != c.SecretRef || pl.Rotate:
			changes[name] = change{send: true}
		}
	}
	src := secrets.For(v.Env.Spec.Secrets, v.Env.Dir)
	values := map[string]string{}
	var missing []string
	for _, name := range slices.Sorted(maps.Keys(changes)) {
		ref := desired[name].SecretRef
		val, ok, err := src.Lookup(ref)
		if err != nil {
			return nil, err
		}
		if !ok {
			missing = append(missing, ref)
			continue
		}
		values[name] = val
	}
	if len(missing) > 0 {
		slices.Sort(missing)
		return nil, fmt.Errorf("secrets not set in %s: %s", src.Describe, strings.Join(slices.Compact(missing), ", "))
	}

	var vaultID string
	meta := map[string]string{metaLogicalID: v.ID(), metaTarget: v.Env.Name}
	if update {
		vaultID = pl.Prior.Binding.HostID
		if old, _ := pl.Prior.Config["name"].(string); old != vaultName(v, s) {
			p.logf("Renaming vault %s to %s\n", vaultID, vaultName(v, s))
			if _, err := p.client.Beta.Vaults.Update(ctx, vaultID, anthropic.BetaVaultUpdateParams{DisplayName: param.NewOpt(vaultName(v, s)), Metadata: updateMetadata(meta)}); err != nil {
				return nil, err
			}
		}
	} else {
		p.logf("Creating vault %s\n", vaultName(v, s))
		vault, err := p.client.Beta.Vaults.New(ctx, anthropic.BetaVaultNewParams{DisplayName: vaultName(v, s), Metadata: meta})
		if err != nil {
			return nil, err
		}
		vaultID = vault.ID
	}

	outputs := map[string]string{}
	applied := func() *provider.Applied {
		return &provider.Applied{Binding: state.Binding{HostID: vaultID, Outputs: maps.Clone(outputs)}}
	}
	// Credentials the Vault dropped, or whose target changed, go first.
	for _, name := range slices.Sorted(maps.Keys(prior)) {
		id := priorOut[outCredentialPrefix+name+".id"]
		if id == "" {
			continue
		}
		if _, keep := desired[name]; keep && !changes[name].create {
			outputs[outCredentialPrefix+name+".id"] = id
			continue
		}
		p.logf("Deleting credential %s (%s) from vault %s\n", name, id, vaultID)
		_, err := p.client.Beta.Vaults.Credentials.Delete(ctx, id, anthropic.BetaVaultCredentialDeleteParams{VaultID: vaultID})
		if err != nil && !isStatus(err, http.StatusNotFound) {
			return applied(), err
		}
	}
	for _, name := range slices.Sorted(maps.Keys(changes)) {
		c, ch := desired[name], changes[name]
		if ch.create {
			p.logf("Adding credential %s to vault %s\n", name, vaultID)
			cred, err := p.client.Beta.Vaults.Credentials.New(ctx, vaultID, anthropic.BetaVaultCredentialNewParams{
				DisplayName: param.NewOpt(name),
				Metadata:    map[string]string{metaLogicalID: v.ID(), "solaria_credential": name},
				Auth:        newAuth(c, values[name]),
			})
			if err != nil {
				return applied(), fmt.Errorf("credential %s: %w", name, err)
			}
			outputs[outCredentialPrefix+name+".id"] = cred.ID
			continue
		}
		id := outputs[outCredentialPrefix+name+".id"]
		p.logf("Sending a new value for credential %s (%s)\n", name, id)
		_, err := p.client.Beta.Vaults.Credentials.Update(ctx, id, anthropic.BetaVaultCredentialUpdateParams{
			VaultID: vaultID,
			Auth:    updateAuth(c, values[name]),
		})
		if err != nil {
			return applied(), fmt.Errorf("credential %s: %w", name, err)
		}
	}

	hash, err := p.vaultHash(ctx, vaultID)
	if err != nil {
		return applied(), err
	}
	a := applied()
	a.RemoteHash = hash
	return a, nil
}

func newAuth(c hostCredential, value string) anthropic.BetaVaultCredentialNewParamsAuthUnion {
	if c.Type == credEnvVar {
		return anthropic.BetaVaultCredentialNewParamsAuthUnion{OfEnvironmentVariable: &anthropic.BetaManagedAgentsEnvironmentVariableCreateParams{
			SecretName:  c.Variable,
			SecretValue: value,
			Networking:  unrestricted(),
		}}
	}
	return anthropic.BetaVaultCredentialNewParamsAuthUnion{OfStaticBearer: &anthropic.BetaManagedAgentsStaticBearerCreateParams{
		MCPServerURL: c.MCPServerURL,
		Token:        bearerToken(value),
	}}
}

func updateAuth(c hostCredential, value string) anthropic.BetaVaultCredentialUpdateParamsAuthUnion {
	if c.Type == credEnvVar {
		return anthropic.BetaVaultCredentialUpdateParamsAuthUnion{OfEnvironmentVariable: &anthropic.BetaManagedAgentsEnvironmentVariableUpdateParams{
			SecretValue: param.NewOpt(value),
		}}
	}
	return anthropic.BetaVaultCredentialUpdateParamsAuthUnion{OfStaticBearer: &anthropic.BetaManagedAgentsStaticBearerUpdateParams{
		Token: param.NewOpt(bearerToken(value)),
	}}
}

// unrestricted lets the host substitute a variable's value on any host the
// environment's network policy allows: sandbox.network decides where the
// agent can send it, as on every other placement.
func unrestricted() anthropic.BetaManagedAgentsCredentialNetworkingParamsUnion {
	return anthropic.BetaManagedAgentsCredentialNetworkingParamsUnion{OfUnrestricted: &anthropic.BetaManagedAgentsUnrestrictedCredentialNetworkingParams{}}
}

var bearerPrefix = regexp.MustCompile(`(?i)^bearer\s+`)

// bearerToken takes the token out of a header value. Launchers send the
// secret as the whole Authorization value, as in "Bearer abc"; the host
// adds "Bearer " itself.
func bearerToken(value string) string {
	return bearerPrefix.ReplaceAllString(strings.TrimSpace(value), "")
}

// priorCredentials reads the credentials a vault was applied with.
func priorCredentials(e *state.VaultEntry) (map[string]hostCredential, error) {
	out := map[string]hostCredential{}
	b, err := json.Marshal(e.Config["credentials"])
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, fmt.Errorf("read %s's applied credentials: %w", e.ID, err)
	}
	return out, nil
}

// vaultHash hashes the vault's live credentials: their IDs and when each
// last changed. Values aren't returned by the host, so a credential whose
// value changed shows as changed only through its update time.
func (p *Provider) vaultHash(ctx context.Context, vaultID string) (string, error) {
	var lines []string
	iter := p.client.Beta.Vaults.Credentials.ListAutoPaging(ctx, vaultID, anthropic.BetaVaultCredentialListParams{})
	for iter.Next() {
		c := iter.Current()
		lines = append(lines, c.ID+" "+c.UpdatedAt.UTC().Format("2006-01-02T15:04:05.000000Z"))
	}
	if err := iter.Err(); err != nil {
		return "", err
	}
	slices.Sort(lines)
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return hex.EncodeToString(sum[:]), nil
}

// ReadVault gets the vault and hashes its credentials. An archived vault
// counts as gone.
func (p *Provider) ReadVault(ctx context.Context, e *state.VaultEntry) (*provider.Observed, error) {
	v, err := p.client.Beta.Vaults.Get(ctx, e.Binding.HostID, anthropic.BetaVaultGetParams{})
	if isStatus(err, http.StatusNotFound) {
		return &provider.Observed{}, nil
	}
	if err != nil {
		return nil, err
	}
	if !v.ArchivedAt.IsZero() {
		return &provider.Observed{}, nil
	}
	hash, err := p.vaultHash(ctx, v.ID)
	if err != nil {
		return nil, err
	}
	return &provider.Observed{Exists: true, RemoteHash: hash}, nil
}

// DestroyVault deletes the vault with its credentials. One that's already
// gone counts as deleted.
func (p *Provider) DestroyVault(ctx context.Context, e *state.VaultEntry) error {
	id := e.Binding.HostID
	p.logf("Deleting vault %s\n", id)
	_, err := p.client.Beta.Vaults.Delete(ctx, id, anthropic.BetaVaultDeleteParams{})
	if err == nil || isStatus(err, http.StatusNotFound) {
		return nil
	}
	return errors.Join(fmt.Errorf("delete vault %s", id), err)
}
