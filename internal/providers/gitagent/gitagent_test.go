package gitagent

import (
	"context"
	"flag"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/renantarouco/solaria/internal/config"
	"github.com/renantarouco/solaria/internal/provider"
	"github.com/renantarouco/solaria/internal/resolve"
	"github.com/renantarouco/solaria/internal/state"
	"github.com/renantarouco/solaria/internal/validate"
)

var update = flag.Bool("update", false, "rewrite golden files")

func load(t *testing.T, dir, env string) *resolve.Agent {
	t.Helper()
	set, diags := config.Load([]string{dir})
	diags = append(diags, validate.Validate(set)...)
	if diags.HasErrors() {
		t.Fatalf("invalid fixture %s:\n%s", dir, diags)
	}
	agents, err := resolve.Environment(set, env)
	if err != nil {
		t.Fatal(err)
	}
	if len(agents) != 1 {
		t.Fatalf("fixture %s has %d agents, want 1", dir, len(agents))
	}
	return &agents[0]
}

// golden renders a build as one text file: every context file, then the
// run command and the diagnostics, with machine-specific paths replaced.
func golden(b *build, root string) string {
	var s strings.Builder
	for _, name := range slices.Sorted(maps.Keys(b.Files)) {
		fmt.Fprintf(&s, "=== %s\n%s", name, b.Files[name])
	}
	fmt.Fprintf(&s, "=== image\n%s\n=== run\n%s\n=== labels\n", b.Image, b.Run)
	for _, k := range slices.Sorted(maps.Keys(b.Labels)) {
		fmt.Fprintf(&s, "%s=%s\n", k, b.Labels[k])
	}
	s.WriteString("=== diagnostics\n")
	s.WriteString(b.Diags.String())
	return strings.ReplaceAll(s.String(), root, "$ROOT")
}

func TestRenderGolden(t *testing.T) {
	for _, tc := range []struct{ fixture, env string }{
		{"full", "local-gitagent"},
		{"open", "dev"},
	} {
		t.Run(tc.fixture, func(t *testing.T) {
			dir, err := filepath.Abs(filepath.Join("testdata", tc.fixture))
			if err != nil {
				t.Fatal(err)
			}
			b, err := render(load(t, dir, tc.env))
			if err != nil {
				t.Fatal(err)
			}
			got := golden(b, filepath.ToSlash(dir))
			path := filepath.Join("testdata", tc.fixture+".golden")
			if *update {
				if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if got != string(want) {
				t.Errorf("render differs from %s (go test -update rewrites it):\n%s", path, got)
			}
		})
	}
}

func TestMixedAllowToolsIsAnError(t *testing.T) {
	a := load(t, filepath.Join("testdata", "full"), "local-gitagent")
	docs := a.Definition.Tools.MCP["docs"]
	docs.AllowTools = nil
	a.Definition.Tools.MCP["docs"] = docs
	b, err := render(a)
	if err != nil {
		t.Fatal(err)
	}
	if !b.Diags.HasErrors() || !strings.Contains(b.Diags.String(), "github list them and docs don't") {
		t.Errorf("diagnostics = %s", b.Diags)
	}
}

func TestPackageThatLooksLikeAnOption(t *testing.T) {
	a := load(t, filepath.Join("testdata", "full"), "local-gitagent")
	a.Env.Sandbox.Packages.APT = []string{"--allow-unauthenticated"}
	b, err := render(a)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.Diags.String(), `error: Agent/reviewer on gitagent/docker: sandbox package "--allow-unauthenticated" starts with '-'`) {
		t.Errorf("diagnostics = %s", b.Diags)
	}
}

// fakeDocker keeps images in memory; building twice gives a new ID.
type fakeDocker struct {
	images map[string]*Image
	builds []BuildRequest
	n      int
}

func (f *fakeDocker) Build(_ context.Context, req BuildRequest, _ io.Writer) error {
	if _, err := os.Stat(req.Dockerfile); err != nil {
		return err
	}
	f.n++
	f.builds = append(f.builds, req)
	f.images[req.Tag] = &Image{ID: fmt.Sprintf("sha256:%02d%062d", f.n, 0), Labels: req.Labels}
	return nil
}

func (f *fakeDocker) Inspect(_ context.Context, ref string) (*Image, error) {
	return f.images[ref], nil
}

func (f *fakeDocker) Remove(_ context.Context, ref string) error {
	delete(f.images, ref)
	return nil
}

// cycle plans and applies one agent the way the engine would, returning
// the plan and the entry it leaves.
func cycle(t *testing.T, p *Provider, a *resolve.Agent, prior *state.Entry) (*provider.Plan, *state.Entry) {
	t.Helper()
	ctx := context.Background()
	pl, err := p.Plan(ctx, a, prior)
	if err != nil {
		t.Fatal(err)
	}
	if pl.Action == provider.NoOp {
		return pl, prior
	}
	applied, err := p.Apply(ctx, pl)
	if err != nil {
		t.Fatal(err)
	}
	return pl, entryFor(a, applied)
}

// entryFor is the state entry the engine would record.
func entryFor(a *resolve.Agent, applied *provider.Applied) *state.Entry {
	return &state.Entry{
		ID: a.ID(), Provider: Name, Placement: PlacementDocker,
		SpecDigest: a.SpecDigest, ResolvedDigest: a.ResolvedDigest,
		Binding: applied.Binding, RemoteHash: applied.RemoteHash, Definition: a.Definition,
	}
}

func TestLifecycle(t *testing.T) {
	work := t.TempDir()
	d := &fakeDocker{images: map[string]*Image{}}
	p := NewWithDocker(provider.Options{Environment: "local-gitagent", WorkDir: work}, d)
	a := load(t, filepath.Join("testdata", "full"), "local-gitagent")
	ctx := context.Background()

	pl, entry := cycle(t, p, a, nil)
	if pl.Action != provider.Create || len(pl.Notes) != 1 {
		t.Fatalf("first plan = %s, notes %v", pl.Action, pl.Notes)
	}
	out := entry.Binding.Outputs
	if entry.Binding.HostID != "solaria/local-gitagent/reviewer" || out[outImage] != "solaria/local-gitagent/reviewer:"+a.ResolvedDigest[7:19] {
		t.Errorf("binding = %+v", entry.Binding)
	}
	if out[outContext] != ".solaria/gitagent/local-gitagent/reviewer" {
		t.Errorf("context output = %q", out[outContext])
	}
	if _, err := os.Stat(filepath.Join(work, out[outContext], "home/.gitagent/skills/triage/labels.txt")); err != nil {
		t.Errorf("skill file not in the build context: %v", err)
	}
	if got := d.builds[0].Labels["sh.solaria.resolved-digest"]; got != a.ResolvedDigest {
		t.Errorf("resolved-digest label = %q", got)
	}

	if pl, _ := cycle(t, p, a, entry); pl.Action != provider.NoOp {
		t.Errorf("unchanged agent plans %s", pl.Action)
	}

	// A sandbox change rebuilds although the digests stay.
	a.Env.Sandbox.Packages.APT = append(a.Env.Sandbox.Packages.APT, "curl")
	pl, entry2 := cycle(t, p, a, entry)
	if pl.Action != provider.Update || pl.Reason != "the environment's sandbox changed" {
		t.Fatalf("sandbox change plans %s (%s)", pl.Action, pl.Reason)
	}
	if paths := changePaths(pl); paths != "sandbox, image" {
		t.Errorf("changes = %s", paths)
	}
	if entry2.Binding.Version == entry.Binding.Version {
		t.Error("rebuild kept the image version")
	}

	// Drift: the image is gone.
	delete(d.images, out[outImage])
	obs, err := p.Read(ctx, entry2)
	if err != nil || obs.Exists {
		t.Errorf("Read after removal = %+v, %v", obs, err)
	}

	// A card change gets a new tag, and the old one is removed.
	a.Definition.Limits.MaxTurns = 10
	a.ResolvedDigest = "sha256:" + strings.Repeat("ab", 32)
	d.images[out[outImage]] = &Image{ID: entry2.RemoteHash}
	pl, entry3 := cycle(t, p, a, entry2)
	if pl.Action != provider.Update || changePaths(pl) != "limits.maxTurns" {
		t.Errorf("card change plans %s with %s", pl.Action, changePaths(pl))
	}
	if _, ok := d.images[out[outImage]]; ok {
		t.Error("the previous image tag wasn't removed")
	}

	if err := p.Destroy(ctx, entry3); err != nil {
		t.Fatal(err)
	}
	if len(d.images) != 0 {
		t.Errorf("images left after destroy: %v", slices.Collect(maps.Keys(d.images)))
	}
	if _, err := os.Stat(filepath.Join(work, out[outContext])); !os.IsNotExist(err) {
		t.Errorf("build context left after destroy: %v", err)
	}
}

func TestSandboxDockerfileIsBuiltFirst(t *testing.T) {
	d := &fakeDocker{images: map[string]*Image{}}
	p := NewWithDocker(provider.Options{Environment: "dev", WorkDir: t.TempDir()}, d)
	a := load(t, filepath.Join("testdata", "open"), "dev")
	_, entry := cycle(t, p, a, nil)
	if len(d.builds) != 2 || !strings.HasPrefix(d.builds[0].Tag, "solaria/dev/helper-sandbox:") {
		t.Fatalf("builds = %+v", d.builds)
	}
	if err := p.Destroy(context.Background(), entry); err != nil {
		t.Fatal(err)
	}
	if len(d.images) != 0 {
		t.Errorf("images left after destroy: %v", slices.Collect(maps.Keys(d.images)))
	}
}

func changePaths(pl *provider.Plan) string {
	var paths []string
	for _, c := range pl.Changes {
		paths = append(paths, c.Path)
	}
	return strings.Join(paths, ", ")
}
