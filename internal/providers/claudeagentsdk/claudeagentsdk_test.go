package claudeagentsdk

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/solaria-sh/solaria/internal/config"
	"github.com/solaria-sh/solaria/internal/provider"
	"github.com/solaria-sh/solaria/internal/providers/process"
	"github.com/solaria-sh/solaria/internal/resolve"
	"github.com/solaria-sh/solaria/internal/state"
	"github.com/solaria-sh/solaria/internal/validate"
)

var update = flag.Bool("update", false, "rewrite golden files")

func load(t *testing.T, dir, env string) *resolve.Agent {
	t.Helper()
	abs, err := filepath.Abs(dir)
	if err != nil {
		t.Fatal(err)
	}
	set, diags := config.Load([]string{abs})
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

// golden renders a bundle as one text file: every file Solaria writes
// for the agent, then the diagnostics, with machine-specific paths
// replaced. The launchers and preludes are the same for every agent, so
// only their names are listed.
func golden(b *bundle, root string) string {
	var s strings.Builder
	shared := map[string][]byte{
		"launch.mjs": launchJS, "launch.py": launchPython,
		"isolate.mjs": process.PreludeJS, "isolate.py": process.PreludePython,
	}
	for _, name := range slices.Sorted(maps.Keys(b.Files)) {
		if c, ok := shared[name]; ok && bytes.Equal(c, b.Files[name]) {
			fmt.Fprintf(&s, "=== %s (shared)\n", name)
			continue
		}
		fmt.Fprintf(&s, "=== %s\n%s", name, b.Files[name])
	}
	fmt.Fprintf(&s, "=== secrets\n%s\n=== diagnostics\n%s", strings.Join(b.Secrets, ","), b.Diags)
	// isolation.json holds the dotenv path in native form, JSON-escaped:
	// on Windows that's C:\\...\\.env rather than /.../.env.
	dotenv, _ := json.Marshal(filepath.Join(root, ".env"))
	out := strings.ReplaceAll(s.String(), string(dotenv), `"$ROOT/.env"`)
	return strings.ReplaceAll(out, filepath.ToSlash(root), "$ROOT")
}

func TestRenderGolden(t *testing.T) {
	for _, tc := range []struct{ fixture, env string }{
		{"full", "dev"},
		{"python", "ci"},
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
			got := golden(b, dir)
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

func TestSettingsErrors(t *testing.T) {
	a := load(t, filepath.Join("testdata", "python"), "ci")
	a.Env.Providers[Name] = map[string]any{"runtime": "deno", "version": "latest"}
	b, err := render(a)
	if err != nil {
		t.Fatal(err)
	}
	got := b.Diags.String()
	for _, want := range []string{
		"providers.claude-agent-sdk.runtime must be node or python, not deno",
		"providers.claude-agent-sdk.version must be an exact package version, such as 0.3.319, not latest",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("diagnostics lack %q:\n%s", want, got)
		}
	}
}

// fakeHost has a runtime of a given version, and installs by writing
// what npm or pip would leave.
type fakeHost struct {
	version  string
	missing  bool
	installs []InstallRequest
}

func (h *fakeHost) GOOS() string { return "linux" }

func (h *fakeHost) Runtime(_ context.Context, name string) (Runtime, error) {
	if h.missing {
		return Runtime{}, fmt.Errorf("%s isn't installed", name)
	}
	return Runtime{Path: "/usr/bin/" + name, Version: h.version}, nil
}

func (h *fakeHost) Install(_ context.Context, req InstallRequest, _ io.Writer) error {
	h.installs = append(h.installs, req)
	files := map[string]string{"package-lock.json": "{}", "node_modules/@anthropic-ai/claude-agent-sdk/package.json": "{}"}
	if req.Runtime == RuntimePython {
		files = map[string]string{"requirements.lock": "claude-agent-sdk==0.2.170\n", "venv/bin/python": ""}
	}
	for name, content := range files {
		path := filepath.Join(req.Dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			return err
		}
	}
	return nil
}

func TestValidate(t *testing.T) {
	a := load(t, filepath.Join("testdata", "python"), "ci")
	for _, tc := range []struct {
		name    string
		host    *fakeHost
		managed []string
		want    []string
	}{
		{"fine", &fakeHost{version: "Python 3.12.1"}, nil, []string{
			"warning: Agent/helper on claude-agent-sdk/process: providers.claude-agent-sdk.colour isn't a setting of this provider and is ignored; it has no per-agent settings",
			"warning: Agent/helper on claude-agent-sdk/process: sandbox.packages aren't installed on the process placement: the run uses the tools this machine has",
			"warning: Agent/helper on claude-agent-sdk/process: sandbox.network is limited, but the process placement doesn't enforce it: the run can reach any host this machine can",
			"warning: Agent/helper on claude-agent-sdk/process: sandbox.resources aren't enforced on the process placement",
		}},
		{"missing", &fakeHost{missing: true}, nil, []string{
			"error: Agent/helper on claude-agent-sdk/process: the python launcher can't run on this machine: python isn't installed",
		}},
		{"old", &fakeHost{version: "Python 3.9.18"}, nil, []string{
			"error: Agent/helper on claude-agent-sdk/process: the python launcher can't run on this machine: /usr/bin/python is Python 3.9.18, and the SDK needs 3.10 or later",
		}},
		{"managed", &fakeHost{version: "Python 3.10.0"}, []string{"/etc/claude-code/managed-mcp.json"}, []string{
			"warning: Agent/helper on claude-agent-sdk/process: managed settings in /etc/claude-code/managed-mcp.json apply to every Claude Code process on this machine and can't be turned off on the process placement; use the docker placement to keep them out",
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := NewWithHost(provider.Options{Environment: "ci", WorkDir: t.TempDir()}, tc.host, func() []string { return tc.managed })
			var got []string
			for _, d := range p.Validate(context.Background(), a) {
				got = append(got, d.String())
			}
			for _, want := range tc.want {
				if !slices.Contains(got, want) {
					t.Errorf("diagnostics lack\n  %s\ngot\n  %s", want, strings.Join(got, "\n  "))
				}
			}
		})
	}
}

func TestCheckRuntime(t *testing.T) {
	for _, tc := range []struct {
		runtime, version string
		ok               bool
	}{
		{RuntimeNode, "v18.14.0", true},
		{RuntimeNode, "v18.13.9", false},
		{RuntimeNode, "v24.1.0", true},
		{RuntimeNode, "v9.99.0", false},
		{RuntimePython, "Python 3.10.0", true},
		{RuntimePython, "Python 3.9.18", false},
		{RuntimePython, "Python 4.0", true},
		{RuntimePython, "", false},
	} {
		if got := checkRuntime(tc.runtime, Runtime{Path: tc.runtime, Version: tc.version}) == ""; got != tc.ok {
			t.Errorf("%s %q: ok = %v, want %v", tc.runtime, tc.version, got, tc.ok)
		}
	}
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
	return pl, &state.Entry{
		ID: a.ID(), Provider: Name, Placement: process.Placement,
		SpecDigest: a.SpecDigest, ResolvedDigest: a.ResolvedDigest,
		Binding: applied.Binding, RemoteHash: applied.RemoteHash, Definition: a.Definition,
	}
}

func changePaths(pl *provider.Plan) string {
	var paths []string
	for _, c := range pl.Changes {
		paths = append(paths, c.Path)
	}
	return strings.Join(paths, ", ")
}

func TestLifecycle(t *testing.T) {
	work := t.TempDir()
	h := &fakeHost{version: "v22.3.0"}
	p := NewWithHost(provider.Options{Environment: "dev", WorkDir: work}, h, func() []string { return nil })
	a := load(t, filepath.Join("testdata", "full"), "dev")
	ctx := context.Background()

	pl, entry := cycle(t, p, a, nil)
	if pl.Action != provider.Create || len(pl.Notes) != 1 || !strings.Contains(pl.Notes[0], "installs @anthropic-ai/claude-agent-sdk@0.3.319 into it with npm") {
		t.Fatalf("first plan = %s, notes %v", pl.Action, pl.Notes)
	}
	out := entry.Binding.Outputs
	bundle := filepath.Join(work, filepath.FromSlash(out[outBundle]))
	if !strings.HasPrefix(out[outBundle], ".solaria/claude-agent-sdk/dev/reviewer/") || entry.Binding.HostID != ".solaria/claude-agent-sdk/dev/reviewer" {
		t.Errorf("binding = %+v", entry.Binding)
	}
	if want := "node " + filepath.Join(bundle, "launch.mjs"); out[outRun] != want && !strings.Contains(out[outRun], "'") {
		t.Errorf("run = %q, want %q", out[outRun], want)
	}
	if out[outRuntime] != "node" || out[outSDKVersion] != DefaultNodeVersion || out[outSecrets] != "DOCS_TOKEN,GITHUB_TOKEN" {
		t.Errorf("outputs = %v", out)
	}
	if len(h.installs) != 1 || h.installs[0].Dir != bundle || h.installs[0].CacheDir != filepath.Join(work, ".solaria", Name, ".cache") {
		t.Errorf("installs = %+v", h.installs)
	}
	if _, err := os.Stat(filepath.Join(bundle, "plugin/skills/triage/labels.txt")); err != nil {
		t.Errorf("skill file not in the bundle: %v", err)
	}
	obs, err := p.Read(ctx, entry)
	if err != nil || !obs.Exists || obs.RemoteHash != entry.RemoteHash {
		t.Errorf("Read = %+v, %v; want the build digest %s", obs, err, entry.RemoteHash)
	}

	if pl, _ := cycle(t, p, a, entry); pl.Action != provider.NoOp {
		t.Errorf("unchanged agent plans %s", pl.Action)
	}

	// Drift: someone edits the bundle.
	if err := os.WriteFile(filepath.Join(bundle, "options.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if obs, _ := p.Read(ctx, entry); !obs.Exists || obs.RemoteHash == entry.RemoteHash {
		t.Errorf("Read after an edit = %+v", obs)
	}

	// Another SDK version gets a new bundle although the digests stay.
	a.Env.Providers = map[string]map[string]any{Name: {"version": "0.3.320"}}
	pl, entry2 := cycle(t, p, a, entry)
	if pl.Action != provider.Update || changePaths(pl) != "sdkVersion, bundle" {
		t.Fatalf("version change plans %s with %s", pl.Action, changePaths(pl))
	}
	if _, err := os.Stat(bundle); !os.IsNotExist(err) {
		t.Errorf("the previous bundle wasn't removed: %v", err)
	}

	// A card change gets a new bundle too, and reports the card's changes.
	a.Definition.Limits.MaxTurns = 10
	a.ResolvedDigest = "sha256:" + strings.Repeat("ab", 32)
	pl, entry3 := cycle(t, p, a, entry2)
	if pl.Action != provider.Update || changePaths(pl) != "limits.maxTurns" {
		t.Errorf("card change plans %s with %s", pl.Action, changePaths(pl))
	}

	// Drift: the SDK is gone.
	bundle3 := filepath.Join(work, filepath.FromSlash(entry3.Binding.Outputs[outBundle]))
	if err := os.RemoveAll(filepath.Join(bundle3, "node_modules")); err != nil {
		t.Fatal(err)
	}
	if obs, err := p.Read(ctx, entry3); err != nil || obs.Exists {
		t.Errorf("Read without the SDK = %+v, %v", obs, err)
	}

	if err := p.Destroy(ctx, entry3); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(work, ".solaria", Name, "dev", "reviewer")); !os.IsNotExist(err) {
		t.Errorf("bundles left after destroy: %v", err)
	}
}

func TestPythonRun(t *testing.T) {
	work := t.TempDir()
	h := &fakeHost{version: "Python 3.12.1"}
	p := NewWithHost(provider.Options{Environment: "ci", WorkDir: work}, h, func() []string { return nil })
	_, entry := cycle(t, p, load(t, filepath.Join("testdata", "python"), "ci"), nil)
	bundle := filepath.Join(work, filepath.FromSlash(entry.Binding.Outputs[outBundle]))
	want := filepath.Join(bundle, "venv", "bin", "python") + " " + filepath.Join(bundle, "launch.py")
	if got := entry.Binding.Outputs[outRun]; got != want && !strings.Contains(got, "'") {
		t.Errorf("run = %q, want %q", got, want)
	}
	if h.installs[0].Exe != "/usr/bin/python" || entry.Binding.Outputs[outSDKVersion] != "0.2.170" {
		t.Errorf("install = %+v, outputs %v", h.installs[0], entry.Binding.Outputs)
	}
	if obs, err := p.Read(context.Background(), entry); err != nil || obs.RemoteHash != entry.RemoteHash {
		t.Errorf("Read = %+v, %v", obs, err)
	}
}

// The fake SDKs answer with what the launcher passed them: the task, the
// options, the environment and the working directory. A task of "fail"
// ends in an error result, and "hang" waits until it's aborted.
const fakeSDKJS = `
export async function* query({ prompt, options }) {
  if (prompt === "hang") {
    await new Promise((_, reject) => options.abortController.signal.addEventListener("abort", () => reject(new Error("aborted"))));
  }
  yield { type: "system", subtype: "init" };
  const { abortController, ...rest } = options;
  yield {
    type: "result",
    subtype: prompt === "fail" ? "error_max_turns" : "success",
    is_error: prompt === "fail",
    result: JSON.stringify({ prompt, options: rest, env: process.env, cwd: process.cwd() }),
    num_turns: 1,
    session_id: "session",
    total_cost_usd: 0.01,
    usage: { input_tokens: 1 },
  };
}
`

const fakeSDKPython = `
import asyncio
import json
import os


class ClaudeAgentOptions:
    def __init__(self, **kwargs):
        self.kwargs = kwargs


class ResultMessage:
    def __init__(self, **kwargs):
        self.__dict__.update(kwargs)


async def query(*, prompt, options):
    if prompt == "hang":
        await asyncio.sleep(60)
    yield {"type": "system"}
    yield ResultMessage(
        subtype="error_max_turns" if prompt == "fail" else "success",
        is_error=prompt == "fail",
        result=json.dumps({"prompt": prompt, "options": options.kwargs, "env": dict(os.environ), "cwd": os.getcwd()}),
        num_turns=1,
        session_id="session",
        total_cost_usd=0.01,
        usage={"input_tokens": 1},
    )
`

// launcher is a runtime that can start a bundle's launcher.
type launcher struct {
	runtime, exe string
	fixture, env string
}

func launchers(t *testing.T) []launcher {
	t.Helper()
	var out []launcher
	if node := works("node"); node != "" {
		out = append(out, launcher{RuntimeNode, node, "full", "dev"})
	}
	pythons := []string{"python3", "python"}
	if runtime.GOOS == "windows" {
		pythons = []string{"python"}
	}
	for _, name := range pythons {
		if py := works(name); py != "" {
			out = append(out, launcher{RuntimePython, py, "python", "ci"})
			break
		}
	}
	if len(out) == 0 {
		t.Skip("neither node nor python is installed")
	}
	return out
}

func works(name string) string {
	path, err := exec.LookPath(name)
	if err != nil || exec.Command(path, "--version").Run() != nil {
		return ""
	}
	return path
}

// setup writes the fixture's bundle with a fake SDK in it, and fake
// package runners for its MCP servers.
func (l launcher) setup(t *testing.T) (bundle, bin, dotenv string) {
	t.Helper()
	work := t.TempDir()
	a := load(t, filepath.Join("testdata", l.fixture), l.env)
	dotenv = filepath.Join(work, ".env")
	a.EnvDir = work // where the dotenv file is

	b, err := render(a)
	if err != nil {
		t.Fatal(err)
	}
	bundle = filepath.Join(work, ".solaria", Name, l.env, a.Name, b.ID())
	if err := writeBundle(bundle, b.Files); err != nil {
		t.Fatal(err)
	}
	sdk := map[string]string{
		"node_modules/@anthropic-ai/claude-agent-sdk/package.json": `{"name": "@anthropic-ai/claude-agent-sdk", "type": "module", "exports": "./index.mjs"}`,
		"node_modules/@anthropic-ai/claude-agent-sdk/index.mjs":    fakeSDKJS,
		"site/claude_agent_sdk/__init__.py":                        fakeSDKPython,
	}
	for name, content := range sdk {
		path := filepath.Join(bundle, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	bin = filepath.Join(work, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range binaries(mustOptions(t, b)) {
		if runtime.GOOS == "windows" {
			name += ".cmd"
		}
		if err := os.WriteFile(filepath.Join(bin, name), []byte("exit 0\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return bundle, bin, dotenv
}

func mustOptions(t *testing.T, b *bundle) sdkOptions {
	t.Helper()
	var bf bundleFile
	if err := json.Unmarshal(b.Files[optionsFile], &bf); err != nil {
		t.Fatal(err)
	}
	return bf.Options
}

// launch runs the launcher with exactly env, and returns its JSON output
// and exit code.
func (l launcher) launch(t *testing.T, bundle, cwd string, env []string, task string) (map[string]any, int) {
	t.Helper()
	args := []string{filepath.Join(bundle, "launch.mjs"), "-p", task}
	if l.runtime == RuntimePython {
		args[0] = filepath.Join(bundle, "launch.py")
		env = append(env, "PYTHONPATH="+filepath.Join(bundle, "site"))
	}
	cmd := exec.Command(l.exe, args...)
	cmd.Env, cmd.Dir = env, cwd
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	code := 0
	if ee := (*exec.ExitError)(nil); errors.As(err, &ee) {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	var res map[string]any
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatalf("%s launcher output %q: %v\n%s", l.runtime, out, err, stderr.String())
	}
	return res, code
}

func TestLaunchers(t *testing.T) {
	for _, l := range launchers(t) {
		t.Run(l.runtime, func(t *testing.T) {
			bundle, bin, dotenv := l.setup(t)
			if err := os.WriteFile(dotenv, []byte("GITHUB_TOKEN=gh-secret\nDOCS_TOKEN=docs-secret\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			cwd := t.TempDir()
			tmp := t.TempDir()
			base := []string{
				"PATH=" + bin + string(os.PathListSeparator) + os.Getenv("PATH"),
				"TMPDIR=" + tmp, "TEMP=" + tmp, "TMP=" + tmp,
				"HOME=" + t.TempDir(),
				"SYSTEMROOT=" + os.Getenv("SYSTEMROOT"),
				"CLAUDE_CONFIG_DIR=/leak", "ANTHROPIC_BASE_URL=https://leak.example.com", "LEAK=1",
			}
			env := append(slices.Clone(base), "ANTHROPIC_API_KEY=sk-test")

			res, code := l.launch(t, bundle, cwd, env, "review it")
			if code != 0 || res["subtype"] != "success" || res["logicalId"] == nil || res["sdkVersion"] == nil || res["session_id"] != "session" {
				t.Fatalf("exit %d, output %v", code, res)
			}
			var seen struct {
				Prompt  string            `json:"prompt"`
				Options map[string]any    `json:"options"`
				Env     map[string]string `json:"env"`
				Cwd     string            `json:"cwd"`
			}
			if err := json.Unmarshal([]byte(res["result"].(string)), &seen); err != nil {
				t.Fatal(err)
			}
			if seen.Prompt != "review it" || !samePath(seen.Cwd, cwd) {
				t.Errorf("prompt %q, cwd %q", seen.Prompt, seen.Cwd)
			}
			for _, k := range []string{"LEAK", "ANTHROPIC_BASE_URL"} {
				if _, ok := seen.Env[k]; ok {
					t.Errorf("%s leaked into the run", k)
				}
			}
			if seen.Env["ANTHROPIC_API_KEY"] != "sk-test" || seen.Env["CLAUDE_CODE_DISABLE_CLAUDE_MDS"] != "1" {
				t.Errorf("env = %v", seen.Env)
			}
			scratch := filepath.Dir(seen.Env["HOME"])
			if !strings.HasPrefix(seen.Env["CLAUDE_CONFIG_DIR"], seen.Env["HOME"]) || !strings.Contains(scratch, "solaria-run-") {
				t.Errorf("HOME %q, CLAUDE_CONFIG_DIR %q", seen.Env["HOME"], seen.Env["CLAUDE_CONFIG_DIR"])
			}
			if _, err := os.Stat(scratch); !os.IsNotExist(err) {
				t.Errorf("scratch directory %s left after the run: %v", scratch, err)
			}

			o := seen.Options
			name := func(ts, py string) string {
				if l.runtime == RuntimePython {
					return py
				}
				return ts
			}
			if src, ok := o[name("settingSources", "setting_sources")].([]any); !ok || len(src) != 0 {
				t.Errorf("setting sources = %v", o[name("settingSources", "setting_sources")])
			}
			if o[name("strictMcpConfig", "strict_mcp_config")] != true || o[name("permissionMode", "permission_mode")] != "dontAsk" {
				t.Errorf("options = %v", o)
			}
			if p, _ := o[name("systemPrompt", "system_prompt")].(string); !strings.Contains(p, "You ") {
				t.Errorf("system prompt = %q", p)
			}
			servers, _ := o[name("mcpServers", "mcp_servers")].(map[string]any)
			if l.runtime == RuntimeNode {
				gh := servers["github"].(map[string]any)["env"].(map[string]any)
				docs := servers["docs"].(map[string]any)["headers"].(map[string]any)
				if gh["GITHUB_PERSONAL_ACCESS_TOKEN"] != "gh-secret" || docs["Authorization"] != "docs-secret" || docs["X-Team"] != "platform" {
					t.Errorf("secrets in MCP servers: %v %v", gh, docs)
				}
				plugins := o["plugins"].([]any)
				if p := plugins[0].(map[string]any)["path"].(string); !samePath(p, filepath.Join(bundle, "plugin")) {
					t.Errorf("plugin path = %q", p)
				}
			} else if servers["notes"].(map[string]any)["command"] != "uvx" {
				t.Errorf("MCP servers = %v", servers)
			}

			if res, code := l.launch(t, bundle, cwd, env, "fail"); code != 1 || res["subtype"] != "error_max_turns" {
				t.Errorf("failing run: exit %d, output %v", code, res)
			}
			if res, code := l.launch(t, bundle, cwd, base, "hi"); code != 2 || res["subtype"] != "error_launch" ||
				!strings.Contains(fmt.Sprint(res["errors"]), "ANTHROPIC_API_KEY isn't set") {
				t.Errorf("run without a key: exit %d, output %v", code, res)
			}

			// A timeout aborts the query.
			var bf map[string]any
			raw, err := os.ReadFile(filepath.Join(bundle, optionsFile))
			if err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(raw, &bf); err != nil {
				t.Fatal(err)
			}
			bf["timeout"], bf["timeoutMs"] = "200ms", 200
			if err := os.WriteFile(filepath.Join(bundle, optionsFile), marshal(bf), 0o644); err != nil {
				t.Fatal(err)
			}
			if res, code := l.launch(t, bundle, cwd, env, "hang"); code != 1 || res["subtype"] != "error_timeout" {
				t.Errorf("hanging run: exit %d, output %v", code, res)
			}
		})
	}
}

func samePath(a, b string) bool {
	ra, err1 := filepath.EvalSymlinks(a)
	rb, err2 := filepath.EvalSymlinks(b)
	return err1 == nil && err2 == nil && ra == rb
}

// npm and pip run inside the bundle, and the run command can run anywhere,
// so a relative project directory must not reach them.
func TestRelativeWorkDir(t *testing.T) {
	a := load(t, filepath.Join("testdata", "full"), "dev")
	work := t.TempDir()
	t.Chdir(work)
	h := &fakeHost{version: "v22.3.0"}
	p := NewWithHost(provider.Options{Environment: "dev", WorkDir: "."}, h, func() []string { return nil })
	_, entry := cycle(t, p, a, nil)
	if req := h.installs[0]; !filepath.IsAbs(req.Dir) || !filepath.IsAbs(req.CacheDir) {
		t.Errorf("install = %+v", req)
	}
	run := entry.Binding.Outputs[outRun]
	if !strings.Contains(run, filepath.Join(work, ".solaria")) && !strings.Contains(run, "'") {
		t.Errorf("run = %q", run)
	}
	if out := entry.Binding.Outputs[outBundle]; !strings.HasPrefix(out, ".solaria/") {
		t.Errorf("bundle output = %q", out)
	}
}
