// Package gitagent deploys agents as container images that run gitagent
// (ADR 0006). Its one placement, docker, builds an image per agent and
// environment with the local docker CLI:
//
//   - the card becomes gitagent's config layer in the image's
//     /root/.gitagent: gitagent.yaml (model, limits, MCP servers),
//     prompts/rules.md (the instructions, after gitagent's own system
//     prompt) and skills/;
//   - the tools become --allowed-tools or --disallowed-tools in the
//     entrypoint, and permissions.ask becomes a denial, since nobody can
//     approve in an unattended run;
//   - the environment's sandbox becomes the image's base and packages
//     (ADR 0007).
//
// The image is tagged solaria/<environment>/<agent>:<resolvedDigest[:12]>
// and labeled with the logical ID and both digests. Apply records a ready
// docker run command as the run output; the task goes after it as -p.
//
// Environments can set providers.gitagent.platform to linux/amd64 (the
// default, as in the cloud sandbox) or linux/arm64.
package gitagent

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/renantarouco/solaria/internal/diag"
	"github.com/renantarouco/solaria/internal/provider"
	"github.com/renantarouco/solaria/internal/providers/container"
	"github.com/renantarouco/solaria/internal/resolve"
	"github.com/renantarouco/solaria/internal/state"
	"github.com/renantarouco/solaria/internal/version"
)

// Name of the provider.
const Name = "gitagent"

// PlacementDocker builds and keeps images in the local Docker engine.
const PlacementDocker = "docker"

// DefaultPlatform matches the cloud sandbox (ADR 0007).
const DefaultPlatform = "linux/amd64"

// Binding outputs.
const (
	outImage         = "image"
	outRun           = "run"
	outContext       = "context"
	outPlatform      = "platform"
	outSandboxImage  = "sandboxImage"
	outSandboxDigest = "sandboxDigest"
	outBuildDigest   = "buildDigest"
	outRunDigest     = "runDigest"
)

// New returns the provider, using the docker CLI.
func New(opts provider.Options) (provider.Provider, error) {
	return NewWithDocker(opts, container.CLI{}), nil
}

// NewWithDocker returns the provider with a given Docker, for tests.
func NewWithDocker(opts provider.Options, d container.Docker) *Provider {
	log := opts.Log
	if log == nil {
		log = io.Discard
	}
	return &Provider{env: opts.Environment, workDir: opts.WorkDir, log: log, docker: d}
}

// Provider is the gitagent provider.
type Provider struct {
	env, workDir string
	log          io.Writer
	docker       container.Docker
}

// Name returns gitagent.
func (p *Provider) Name() string { return Name }

// Version is the Solaria version; the gitagent commit is in each image's
// labels and digest.
func (p *Provider) Version() string { return version.Version }

// Placements returns docker.
func (p *Provider) Placements() []string { return []string{PlacementDocker} }

// Capabilities of gitagent in a container. gitagent runs models from any
// provider it has an adapter for, falls back between them, and connects
// over every MCP transport. Nobody can answer an approval in a container,
// so permissions.ask means deny.
func (p *Provider) Capabilities(string) provider.Capabilities {
	return provider.Capabilities{
		Fallback:      true,
		MCPTransports: []string{provider.TransportStdio, provider.TransportStreamableHTTP, provider.TransportSSE},
		SandboxImage:  true,
		Destroy:       provider.DestroyDelete,
	}
}

// Validate renders the agent and reports what gitagent can't express.
func (p *Provider) Validate(_ context.Context, a *resolve.Agent) []diag.Diagnostic {
	b, err := render(a)
	if err != nil {
		return []diag.Diagnostic{diag.Errorf("", diag.Pos{}, "%s on %s/%s: %v", a.ID(), Name, PlacementDocker, err)}
	}
	return b.Diags
}

// Plan starts from the default plan and adds what the digests don't
// cover: the environment's sandbox, the generated image and the run
// command can change while the agent's definition stays the same.
func (p *Provider) Plan(_ context.Context, desired *resolve.Agent, prior *state.Entry) (*provider.Plan, error) {
	pl := provider.DefaultPlan(desired, prior)
	b, err := render(desired)
	if err != nil {
		return nil, err
	}
	switch pl.Action {
	case provider.Create:
		pl.Notes = append(pl.Notes, fmt.Sprintf("builds %s for %s; the first build compiles gitagent and the sandbox base, which takes several minutes", b.Image, b.Platform))
	case provider.NoOp:
		if changes := componentChanges(prior.Binding.Outputs, b); len(changes) > 0 {
			pl.Action = provider.Update
			pl.Changes = changes
			pl.Reason = "the image's inputs changed although the agent didn't"
			if changes[0].Path == "sandbox" {
				pl.Reason = "the environment's sandbox changed"
			}
		}
	case provider.Update:
		// The image and run command always change with the agent; only a
		// sandbox change is news.
		for _, c := range componentChanges(prior.Binding.Outputs, b) {
			if c.Path == "sandbox" {
				pl.Changes = append(pl.Changes, c)
			}
		}
	}
	return pl, nil
}

// Apply writes the build context, builds the image and removes the image
// it replaces.
func (p *Provider) Apply(ctx context.Context, pl *provider.Plan) (*provider.Applied, error) {
	a := pl.Desired
	b, err := render(a)
	if err != nil {
		return nil, err
	}
	dir := p.contextDir(a.Name)
	if err := writeContext(dir, b.Files); err != nil {
		return nil, fmt.Errorf("gitagent: write build context: %w", err)
	}
	if b.Base != nil {
		p.logf("Building the sandbox image %s from %s\n", b.BaseImage, b.Base.Dockerfile)
		if err := p.docker.Build(ctx, *b.Base, p.log); err != nil {
			return nil, err
		}
	}
	p.logf("Building %s for %s\n", b.Image, b.Platform)
	err = p.docker.Build(ctx, container.BuildRequest{
		Context:    dir,
		Dockerfile: filepath.Join(dir, dockerfilePath),
		Tag:        b.Image,
		Platform:   b.Platform,
		Labels:     b.Labels,
	}, p.log)
	if err != nil {
		return nil, err
	}
	img, err := p.docker.Inspect(ctx, b.Image)
	if err != nil {
		return nil, err
	}
	if img == nil {
		return nil, fmt.Errorf("gitagent: %s is missing right after building it", b.Image)
	}
	if pl.Prior != nil {
		p.removeStale(ctx, pl.Prior, b)
	}

	rel, err := filepath.Rel(p.workDir, dir)
	if err != nil {
		rel = dir
	}
	outputs := map[string]string{
		outImage:         b.Image,
		outRun:           b.Run,
		outContext:       filepath.ToSlash(rel),
		outPlatform:      b.Platform,
		outSandboxDigest: b.SandboxDigest,
		outBuildDigest:   b.BuildDigest,
		outRunDigest:     b.RunDigest,
	}
	if b.BaseImage != "" {
		outputs[outSandboxImage] = b.BaseImage
	}
	return &provider.Applied{
		Binding:    state.Binding{HostID: b.Repository, Version: shortID(img.ID), Outputs: outputs},
		RemoteHash: img.ID,
	}, nil
}

// removeStale removes the prior images this apply no longer uses. They
// would only take space; failing to remove one isn't an apply failure.
func (p *Provider) removeStale(ctx context.Context, prior *state.Entry, b *build) {
	for key, now := range map[string]string{outImage: b.Image, outSandboxImage: b.BaseImage} {
		if old := prior.Binding.Outputs[key]; old != "" && old != now {
			if err := p.docker.Remove(ctx, old); err != nil {
				p.logf("warning: couldn't remove the previous image: %v\n", err)
			}
		}
	}
}

// Read inspects the entry's image. Its ID is the remote hash, so a rebuild
// or retag outside Solaria shows up as drift.
func (p *Provider) Read(ctx context.Context, e *state.Entry) (*provider.Observed, error) {
	img, err := p.docker.Inspect(ctx, e.Binding.Outputs[outImage])
	if err != nil {
		return nil, err
	}
	if img == nil {
		return &provider.Observed{Exists: false}, nil
	}
	return &provider.Observed{Exists: true, RemoteHash: img.ID}, nil
}

// Destroy removes the entry's images and build context.
func (p *Provider) Destroy(ctx context.Context, e *state.Entry) error {
	for _, key := range []string{outImage, outSandboxImage} {
		if ref := e.Binding.Outputs[key]; ref != "" {
			if err := p.docker.Remove(ctx, ref); err != nil {
				return err
			}
		}
	}
	_, name, _ := strings.Cut(e.ID, "/")
	if err := os.RemoveAll(p.contextDir(name)); err != nil {
		return fmt.Errorf("gitagent: remove build context: %w", err)
	}
	return nil
}

// logf writes progress. Progress that can't be written is lost, not an
// apply failure.
func (p *Provider) logf(format string, args ...any) {
	_, _ = fmt.Fprintf(p.log, format, args...)
}

// contextDir is where an agent's build context is written. It stays after
// apply, so the rendered files can be inspected.
func (p *Provider) contextDir(agent string) string {
	return filepath.Join(p.workDir, ".solaria", Name, p.env, agent)
}

// writeContext replaces dir with files, so files the card no longer has
// don't linger in the next image.
func writeContext(dir string, files map[string][]byte) error {
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	for name, content := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, content, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// shortID shortens an image ID the way docker images shows it.
func shortID(id string) string {
	id = strings.TrimPrefix(id, "sha256:")
	if len(id) > 12 {
		return id[:12]
	}
	return id
}
