// Package stub is a provider that deploys to nothing but files, in the
// spirit of Terraform's null provider. It exercises the whole plan and apply
// cycle without a host or credentials, which makes it the provider for
// examples and end-to-end tests. (It isn't called null because YAML reads an
// unquoted null as no value.)
//
// Its one placement, file, keeps each "host object" as a JSON file under
// .solaria/stub/<environment>/. Editing or deleting those files simulates
// changes made outside Solaria.
//
// For tests, an agent can ask it to fail: providers.stub.fail set to apply
// or destroy makes that operation return an error.
package stub

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/solaria-sh/solaria/internal/diag"
	"github.com/solaria-sh/solaria/internal/digest"
	"github.com/solaria-sh/solaria/internal/provider"
	"github.com/solaria-sh/solaria/internal/resolve"
	"github.com/solaria-sh/solaria/internal/state"
	"github.com/solaria-sh/solaria/internal/version"
)

// Name of the provider.
const Name = "stub"

// PlacementFile keeps host objects in files.
const PlacementFile = "file"

// New returns the provider.
func New(opts provider.Options) (provider.Provider, error) {
	return &Provider{dir: filepath.Join(opts.WorkDir, ".solaria", "stub", opts.Environment), env: opts.Environment}, nil
}

// Provider is the stub provider.
type Provider struct {
	dir string
	env string
}

// Name returns stub.
func (p *Provider) Name() string { return Name }

// Version is the Solaria version: the stub provider ships with it.
func (p *Provider) Version() string { return version.Version }

// Placements returns file.
func (p *Provider) Placements() []string { return []string{PlacementFile} }

// Capabilities accepts everything, so tests can use any card.
func (p *Provider) Capabilities(string) provider.Capabilities {
	return provider.Capabilities{
		Fallback:             true,
		MCPTransports:        []string{provider.TransportStdio, provider.TransportStreamableHTTP, provider.TransportSSE},
		SandboxImage:         true,
		LocalWorkspace:       true,
		Repositories:         provider.RepositoriesClone,
		ReadOnlyRepositories: true,
		Approvals:            true,
		OutputSchema:         true,
		Destroy:              provider.DestroyDelete,
	}
}

// Validate has nothing to add.
func (p *Provider) Validate(context.Context, *resolve.Agent) []diag.Diagnostic { return nil }

// Plan uses the default plan.
func (p *Provider) Plan(_ context.Context, desired *resolve.Agent, prior *state.Entry) (*provider.Plan, error) {
	return provider.DefaultPlan(desired, prior), nil
}

// object is what the stub "host" stores.
type object struct {
	ID             string             `json:"id"`
	Version        int                `json:"version"`
	LogicalID      string             `json:"logicalId"`
	SpecDigest     string             `json:"specDigest"`
	ResolvedDigest string             `json:"resolvedDigest"`
	Definition     resolve.Definition `json:"definition"`
}

// Apply writes the object file, bumping its version on every update.
func (p *Provider) Apply(_ context.Context, pl *provider.Plan) (*provider.Applied, error) {
	a := pl.Desired
	if fail(a.Definition, "apply") {
		return nil, fmt.Errorf("stub: failing apply of %s as providers.stub.fail asks", a.ID())
	}
	obj := object{
		ID:             hostID(p.env, a.Name),
		Version:        1,
		LogicalID:      a.ID(),
		SpecDigest:     a.SpecDigest,
		ResolvedDigest: a.ResolvedDigest,
		Definition:     a.Definition,
	}
	if pl.Prior != nil && pl.Action == provider.Update {
		if v, err := strconv.Atoi(pl.Prior.Binding.Version); err == nil {
			obj.Version = v + 1
		}
	}
	b, err := json.MarshalIndent(obj, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(p.dir, 0o755); err != nil {
		return nil, fmt.Errorf("stub: %w", err)
	}
	path := p.path(a.Name)
	if err := os.WriteFile(path, append(b, '\n'), 0o644); err != nil {
		return nil, fmt.Errorf("stub: %w", err)
	}
	hash, err := remoteHash(b)
	if err != nil {
		return nil, err
	}
	return &provider.Applied{
		Binding: state.Binding{
			HostID:  obj.ID,
			Version: strconv.Itoa(obj.Version),
			Outputs: map[string]string{"file": filepath.ToSlash(path)},
		},
		RemoteHash: hash,
	}, nil
}

// Read reads the object file back.
func (p *Provider) Read(_ context.Context, e *state.Entry) (*provider.Observed, error) {
	b, err := os.ReadFile(p.path(name(e.ID)))
	if errors.Is(err, fs.ErrNotExist) {
		return &provider.Observed{Exists: false}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("stub: %w", err)
	}
	hash, err := remoteHash(b)
	if err != nil {
		return nil, fmt.Errorf("stub: %s: %w", p.path(name(e.ID)), err)
	}
	return &provider.Observed{Exists: true, RemoteHash: hash}, nil
}

// Destroy removes the object file.
func (p *Provider) Destroy(_ context.Context, e *state.Entry) error {
	if fail(e.Definition, "destroy") {
		return fmt.Errorf("stub: failing destroy of %s as providers.stub.fail asks", e.ID)
	}
	err := os.Remove(p.path(name(e.ID)))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("stub: %w", err)
	}
	return nil
}

func (p *Provider) path(agent string) string { return filepath.Join(p.dir, agent+".json") }

func name(id string) string {
	_, n, _ := strings.Cut(id, "/")
	return n
}

// hostID is stable per environment and agent, as a host's ID would be.
func hostID(env, agent string) string {
	sum := sha256.Sum256([]byte(env + "/" + agent))
	return "stub_" + hex.EncodeToString(sum[:6])
}

// remoteHash hashes the object's canonical JSON, so reformatting the file
// isn't drift but changing a value is.
func remoteHash(b []byte) (string, error) {
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return "", err
	}
	return digest.Of(v)
}

func fail(d resolve.Definition, op string) bool {
	return d.Providers[Name]["fail"] == op
}
