// Package claudeagentsdk deploys agents that run on the Claude Agent SDK
// (ADR 0014). Its placement, process, runs the agent loop as a local Node
// or Python process. Solaria never starts that process (ADR 0002); apply
// writes a bundle per agent and environment that the caller starts:
//
//   - options.json: the SDK options the definition maps to, with secrets as
//     references;
//   - system-prompt.md: the instructions, as one custom system prompt;
//   - plugin/: the skills, as a local plugin named after the agent;
//   - isolation.json and the prelude (internal/providers/process), which
//     make every run hermetic;
//   - launch.mjs or launch.py, and the SDK installed at an exact version.
//
// Bundles live in .solaria/claude-agent-sdk/<env>/<agent>/<id>/, where id
// is the first 12 hex digits of the bundle's build digest. Apply records
// the command that starts the launcher as the run output.
//
// Environments can set providers.claude-agent-sdk.runtime to node (the
// default) or python, and providers.claude-agent-sdk.version to install
// another SDK version than the one Solaria was tested with.
package claudeagentsdk

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/solaria-sh/solaria/internal/diag"
	"github.com/solaria-sh/solaria/internal/digest"
	"github.com/solaria-sh/solaria/internal/provider"
	"github.com/solaria-sh/solaria/internal/providers/container"
	"github.com/solaria-sh/solaria/internal/providers/process"
	"github.com/solaria-sh/solaria/internal/resolve"
	"github.com/solaria-sh/solaria/internal/state"
	"github.com/solaria-sh/solaria/internal/version"
)

// Name of the provider.
const Name = "claude-agent-sdk"

// Binding outputs.
const (
	outRun         = "run"
	outBundle      = "bundle"
	outRuntime     = "runtime"
	outSDKVersion  = "sdkVersion"
	outBuildDigest = "buildDigest"
	outSecrets     = "secrets"
)

// New returns the provider, using this machine's runtimes and the docker
// CLI.
func New(opts provider.Options) (provider.Provider, error) {
	return NewWithHost(opts, localHost{}, container.CLI{}, process.ManagedSettings), nil
}

// NewWithHost returns the provider with a given host, Docker and managed
// settings finder, for tests.
func NewWithHost(opts provider.Options, h Host, d container.Docker, managed func() []string) *Provider {
	log := opts.Log
	if log == nil {
		log = io.Discard
	}
	// Bundle paths go into the run command and to npm and pip, which run
	// in the bundle, so they must not be relative.
	workDir := opts.WorkDir
	if abs, err := filepath.Abs(workDir); err == nil {
		workDir = abs
	}
	return &Provider{env: opts.Environment, workDir: workDir, log: log, host: h, docker: d, managed: managed}
}

// Provider is the claude-agent-sdk provider.
type Provider struct {
	env, workDir string
	log          io.Writer
	host         Host
	docker       container.Docker
	managed      func() []string
}

// Name returns claude-agent-sdk.
func (p *Provider) Name() string { return Name }

// Version is the Solaria version; the SDK version is in each binding's
// outputs.
func (p *Provider) Version() string { return version.Version }

// Placements returns process and docker.
func (p *Provider) Placements() []string { return []string{process.Placement, PlacementDocker} }

// Capabilities of the SDK, in a local process or a container. It runs
// Anthropic models only, falls back between them, and connects over every
// MCP transport. Nobody can answer an approval in an unattended run, so
// permissions.ask means deny. Only docker takes a sandbox image. It
// enforces an output schema. Destroy removes the bundle, and the images on
// docker.
func (p *Provider) Capabilities(placement string) provider.Capabilities {
	return provider.Capabilities{
		ModelProviders: []string{"anthropic"},
		Fallback:       true,
		MCPTransports:  []string{provider.TransportStdio, provider.TransportStreamableHTTP, provider.TransportSSE},
		SandboxImage:   placement == PlacementDocker,
		OutputSchema:   true,
		Destroy:        provider.DestroyDelete,
	}
}

// Validate renders the agent. On the process placement it also checks
// that this machine has the runtime, and warns about what a local process
// can't isolate or enforce; a container has its own runtime and doesn't
// see the machine's managed settings.
func (p *Provider) Validate(ctx context.Context, a *resolve.Agent) []diag.Diagnostic {
	where := fmt.Sprintf("%s on %s/%s", a.ID(), Name, a.Placement)
	b, err := render(a)
	if err != nil {
		return []diag.Diagnostic{diag.Errorf("", diag.Pos{}, "%s: %v", where, err)}
	}
	diags := b.Diags
	if a.Placement == PlacementDocker {
		return diags
	}
	if !diags.HasErrors() {
		rt, err := p.host.Runtime(ctx, b.Runtime)
		why := ""
		if err != nil {
			why = err.Error()
		} else {
			why = checkRuntime(b.Runtime, rt)
		}
		if why != "" {
			diags = append(diags, diag.Errorf("", diag.Pos{}, "%s: the %s launcher can't run on this machine: %s", where, b.Runtime, why))
		}
	}
	for _, d := range process.Validate(a, p.managed()) {
		d.Message = where + ": " + d.Message
		diags = append(diags, d)
	}
	return diags
}

// Plan starts from the default plan and adds what the digests don't
// cover: the runtime, the SDK version and Solaria's launcher can change
// while the agent's definition stays the same.
func (p *Provider) Plan(_ context.Context, desired *resolve.Agent, prior *state.Entry) (*provider.Plan, error) {
	pl := provider.DefaultPlan(desired, prior)
	b, err := render(desired)
	if err != nil {
		return nil, err
	}
	switch pl.Action {
	case provider.Create:
		if d := b.Docker; d != nil {
			pl.Notes = append(pl.Notes, fmt.Sprintf("builds %s for %s with %s@%s; the first build installs the sandbox base and the SDK, which takes several minutes",
				d.Image, d.Platform, sdkPackage(b.Runtime), b.SDKVersion))
			break
		}
		pl.Notes = append(pl.Notes, fmt.Sprintf("writes a bundle to %s and installs %s@%s into it with %s, which needs network access",
			p.rel(p.bundleDir(desired.Name, b)), sdkPackage(b.Runtime), b.SDKVersion, installer(b.Runtime)))
	case provider.NoOp:
		if changes := componentChanges(prior.Binding.Outputs, b); len(changes) > 0 {
			pl.Action = provider.Update
			pl.Changes = changes
			pl.Reason = "the bundle's inputs changed although the agent didn't"
		}
	case provider.Update:
		// The bundle always changes with the agent; only the runtime and
		// SDK version are news.
		for _, c := range componentChanges(prior.Binding.Outputs, b) {
			if c.Path != "bundle" {
				pl.Changes = append(pl.Changes, c)
			}
		}
	}
	return pl, nil
}

func installer(runtime string) string {
	if runtime == RuntimePython {
		return "pip"
	}
	return "npm"
}

// Apply writes the bundle, then installs the SDK into it, or on docker
// builds the image from it, and removes the agent's other bundles.
func (p *Provider) Apply(ctx context.Context, pl *provider.Plan) (*provider.Applied, error) {
	a := pl.Desired
	b, err := render(a)
	if err != nil {
		return nil, err
	}
	dir := p.bundleDir(a.Name, b)
	if err := writeBundle(dir, b.Files); err != nil {
		return nil, fmt.Errorf("%s: write bundle: %w", Name, err)
	}
	if b.Docker != nil {
		return p.applyDocker(ctx, pl, b, dir)
	}
	rt, err := p.host.Runtime(ctx, b.Runtime)
	if err != nil {
		return nil, err
	}
	p.logf("Installing %s@%s into %s\n", sdkPackage(b.Runtime), b.SDKVersion, p.rel(dir))
	cache := filepath.Join(p.providerDir(), ".cache")
	if err := p.host.Install(ctx, InstallRequest{Runtime: b.Runtime, Exe: rt.Path, Dir: dir, CacheDir: cache}, p.log); err != nil {
		return nil, fmt.Errorf("%s: install the SDK: %w", Name, err)
	}
	p.removeOthers(a.Name, b.ID())
	inv := b.invocation(p.argv(dir, b), state.WorkspaceCwd)
	return &provider.Applied{
		Binding: state.Binding{
			HostID: p.rel(p.agentDir(a.Name)), Version: b.ID(), Outputs: p.outputs(b, dir, inv), Invocation: inv,
			Runtime: b.runtime(),
		},
		RemoteHash: b.BuildDigest,
	}, nil
}

// outputs are the binding outputs both placements record.
func (p *Provider) outputs(b *bundle, dir string, inv *state.Invocation) map[string]string {
	out := map[string]string{
		outRun:         container.ShellCommand(inv.Argv),
		outBundle:      p.rel(dir),
		outRuntime:     b.Runtime,
		outSDKVersion:  b.SDKVersion,
		outBuildDigest: b.BuildDigest,
	}
	if len(b.Secrets) > 0 {
		out[outSecrets] = strings.Join(b.Secrets, ",")
	}
	return out
}

// runtime names the SDK package at the version the bundle installs, for
// the binding.
func (b *bundle) runtime() string {
	return sdkPackage(b.Runtime) + "@" + b.SDKVersion
}

// argv is the command that starts the launcher. It runs in the workspace
// the agent works on, which is the current directory, so the bundle's
// paths are absolute.
func (p *Provider) argv(dir string, b *bundle) []string {
	if b.Runtime == RuntimePython {
		return []string{venvPython(p.host.GOOS(), dir), filepath.Join(dir, "launch.py")}
	}
	return []string{"node", filepath.Join(dir, "launch.mjs")}
}

// ResultFormat names what the launcher prints: one JSON object with the
// SDK's result and the agent's identity.
const ResultFormat = "solaria.sh/result/v1"

// invocation records how to start the launcher: the task goes after argv
// with -p, or on stdin; the API key and the secrets come from the caller's
// environment, or the secrets from the dotenv file.
func (b *bundle) invocation(argv []string, workspace string) *state.Invocation {
	env := []string{"ANTHROPIC_API_KEY"}
	if b.Dotenv == "" {
		env = append(env, b.Secrets...)
	}
	return &state.Invocation{
		Argv:      argv,
		Workspace: workspace,
		Task:      state.TaskInput{Flag: "-p", Stdin: true},
		Env:       env,
		EnvFile:   b.Dotenv,
		Result:    ResultFormat,
	}
}

// removeOthers removes the agent's bundles other than keep: the one this
// apply replaced, and any an interrupted apply left. They would only take
// space; failing to remove one isn't an apply failure.
func (p *Provider) removeOthers(agent, keep string) {
	entries, err := os.ReadDir(p.agentDir(agent))
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.Name() == keep {
			continue
		}
		if err := os.RemoveAll(filepath.Join(p.agentDir(agent), e.Name())); err != nil {
			p.logf("warning: couldn't remove the previous bundle: %v\n", err)
		}
	}
}

// Read checks the entry's bundle. Its remote hash is the digest of the
// bundle's files as they are on disk, so editing one shows up as drift. A
// bundle whose SDK isn't installed doesn't exist.
func (p *Provider) Read(ctx context.Context, e *state.Entry) (*provider.Observed, error) {
	if e.Placement == PlacementDocker {
		return p.readDocker(ctx, e)
	}
	dir := filepath.Join(p.workDir, filepath.FromSlash(e.Binding.Outputs[outBundle]))
	installed := filepath.Join(dir, "node_modules", filepath.FromSlash(NodePackage), "package.json")
	if e.Binding.Outputs[outRuntime] == RuntimePython {
		installed = venvPython(p.host.GOOS(), dir)
	}
	if _, err := os.Stat(installed); errors.Is(err, fs.ErrNotExist) {
		return &provider.Observed{Exists: false}, nil
	}
	hash, err := bundleDigest(dir)
	if err != nil {
		return nil, fmt.Errorf("%s: read bundle: %w", Name, err)
	}
	return &provider.Observed{Exists: true, RemoteHash: hash}, nil
}

// installed are the files and folders apply's install adds to a bundle,
// and what running it adds; the bundle's digest leaves them out.
var installed = []string{"node_modules", "package-lock.json", "venv", "requirements.lock", "__pycache__"}

// bundleDigest hashes the bundle's files the way render does.
func bundleDigest(dir string) (string, error) {
	files := map[string]string{}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		if slices.Contains(installed, filepath.ToSlash(rel)) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		files[filepath.ToSlash(rel)] = digest.Bytes(content)
		return nil
	})
	if err != nil {
		return "", err
	}
	return digest.Of(files)
}

// Destroy removes the agent's bundles, and its images on docker.
func (p *Provider) Destroy(ctx context.Context, e *state.Entry) error {
	if e.Placement == PlacementDocker {
		if err := p.destroyDocker(ctx, e); err != nil {
			return err
		}
	}
	_, name, _ := strings.Cut(e.ID, "/")
	if err := os.RemoveAll(p.agentDir(name)); err != nil {
		return fmt.Errorf("%s: remove bundle: %w", Name, err)
	}
	return nil
}

// logf writes progress. Progress that can't be written is lost, not an
// apply failure.
func (p *Provider) logf(format string, args ...any) {
	_, _ = fmt.Fprintf(p.log, format, args...)
}

func (p *Provider) providerDir() string {
	return filepath.Join(p.workDir, ".solaria", Name)
}

func (p *Provider) agentDir(agent string) string {
	return filepath.Join(p.providerDir(), p.env, agent)
}

// bundleDir is where an agent's bundle is written. It stays until the
// agent changes or is destroyed.
func (p *Provider) bundleDir(agent string, b *bundle) string {
	return filepath.Join(p.agentDir(agent), b.ID())
}

// rel is a path relative to the project, with forward slashes, for
// outputs and messages.
func (p *Provider) rel(path string) string {
	rel, err := filepath.Rel(p.workDir, path)
	if err != nil {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(rel)
}

// writeBundle replaces dir with files, so files the card no longer has
// don't linger, and an install that was interrupted starts over.
func writeBundle(dir string, files map[string][]byte) error {
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

// componentChanges compares a prior binding with a new bundle.
func componentChanges(outputs map[string]string, b *bundle) []provider.Change {
	var out []provider.Change
	for _, c := range []struct {
		path, key, now string
		digest         bool
	}{
		{"runtime", outRuntime, b.Runtime, false},
		{"sdkVersion", outSDKVersion, b.SDKVersion, false},
		{"bundle", outBuildDigest, b.BuildDigest, true},
	} {
		old := outputs[c.key]
		if old == c.now {
			continue
		}
		if old == "" {
			old = "none"
		}
		ch := provider.Change{Path: c.path, Old: old, New: c.now}
		if c.digest {
			ch.Old, ch.New = provider.ShortDigest(old), provider.ShortDigest(c.now)
		}
		out = append(out, ch)
	}
	return out
}
