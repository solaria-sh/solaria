package process

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/solaria-sh/solaria/internal/api/v1alpha1"
	"github.com/solaria-sh/solaria/internal/resolve"
	"github.com/solaria-sh/solaria/internal/secrets"
)

var update = flag.Bool("update", false, "rewrite golden files")

// agent is an agent whose MCP server needs MCP_TOKEN, from a dotenv file.
func agent(dir string) *resolve.Agent {
	return &resolve.Agent{
		Name:   "triage",
		EnvDir: dir,
		Env: &v1alpha1.EnvironmentSpec{
			Secrets: &v1alpha1.SecretsSpec{Source: v1alpha1.SecretsDotenv, Path: "secrets.env"},
		},
		Definition: resolve.Definition{Tools: resolve.Tools{MCP: map[string]resolve.MCPServer{
			"tracker": {Spec: v1alpha1.McpServerSpec{Packages: []v1alpha1.MCPPackage{{
				EnvironmentVariables: []v1alpha1.KeyValue{{Name: "TOKEN", SecretRef: "MCP_TOKEN"}},
			}}}},
		}}},
	}
}

var needs = Needs{
	Binaries: []string{"npx"},
	Pass:     []string{"ANTHROPIC_API_KEY"},
	Set:      map[string]string{"CLAUDE_CODE_DISABLE_AUTO_MEMORY": "1"},
	Scratch:  map[string]string{"CLAUDE_CONFIG_DIR": "home/.claude"},
}

func TestFor(t *testing.T) {
	dir := t.TempDir()
	iso := For(agent(dir), needs)
	if want := filepath.Join(dir, "secrets.env"); iso.Dotenv != want {
		t.Errorf("Dotenv = %q, want %q", iso.Dotenv, want)
	}
	// The dotenv path depends on the machine; the rest is fixed.
	iso.Dotenv = "/project/secrets.env"
	golden(t, "testdata/isolation.golden.json", iso.JSON())

	// Without a dotenv source, secrets come from the caller's environment.
	a := agent(dir)
	a.Env.Secrets = nil
	if iso := For(a, Needs{}); iso.Dotenv != "" || !slices.Equal(iso.Secrets, []string{"MCP_TOKEN"}) {
		t.Errorf("env source: Dotenv %q, Secrets %v", iso.Dotenv, iso.Secrets)
	}
}

func golden(t *testing.T, path string, got []byte) {
	t.Helper()
	if *update {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("%s differs; run go test -update and review the diff:\n%s", path, got)
	}
}

func TestValidate(t *testing.T) {
	a := agent(t.TempDir())
	got := messages(Validate(a, nil))
	if len(got) != 1 || !strings.Contains(got[0], "sandbox.network is limited") {
		t.Errorf("default sandbox: %q", got)
	}

	a.Env.Sandbox = &v1alpha1.SandboxSpec{
		Packages:  &v1alpha1.PackagesSpec{NPM: []string{"left-pad"}},
		Network:   &v1alpha1.NetworkSpec{Mode: v1alpha1.NetworkUnrestricted},
		Resources: &v1alpha1.ResourcesSpec{Memory: "4Gi"},
	}
	got = messages(Validate(a, []string{"/etc/claude-code/managed-settings.json"}))
	want := []string{
		"sandbox.packages aren't installed on the process placement: the run uses the tools this machine has",
		"sandbox.resources aren't enforced on the process placement",
		"managed settings in /etc/claude-code/managed-settings.json apply to every Claude Code process on this machine and can't be turned off on the process placement; use the docker placement to keep them out",
	}
	if !slices.Equal(got, want) {
		t.Errorf("got  %q\nwant %q", got, want)
	}
}

func messages[T interface{ String() string }](diags []T) []string {
	var out []string
	for _, d := range diags {
		_, msg, _ := strings.Cut(d.String(), "warning: ")
		out = append(out, msg)
	}
	return out
}

func TestManagedSources(t *testing.T) {
	files := map[string]bool{}
	m := machine{
		exists: func(p string) bool { return files[p] },
		glob: func(pattern string) ([]string, error) {
			var out []string
			for p := range files {
				if ok, _ := filepath.Match(pattern, p); ok {
					out = append(out, p)
				}
			}
			return out, nil
		},
		getenv: func(string) string { return "" },
	}

	for _, goos := range []string{"darwin", "linux", "windows"} {
		m.goos = goos
		if got := managedSources(m); len(got) != 0 {
			t.Errorf("%s, nothing installed: %q", goos, got)
		}
	}

	m.goos = "linux"
	dir := systemDir(m)
	for _, p := range []string{
		filepath.Join(dir, "managed-settings.json"),
		filepath.Join(dir, "managed-mcp.json"),
		filepath.Join(dir, "managed-settings.d", "10-security.json"),
		filepath.Join(dir, "managed-settings.d", ".hidden.json"),
		filepath.Join(dir, "managed-settings.d", "notes.txt"),
	} {
		files[p] = true
	}
	want := []string{
		filepath.Join(dir, "managed-mcp.json"),
		filepath.Join(dir, "managed-settings.d", "10-security.json"),
		filepath.Join(dir, "managed-settings.json"),
	}
	if got := managedSources(m); !slices.Equal(got, want) {
		t.Errorf("linux:\ngot  %q\nwant %q", got, want)
	}

	clear(files)
	m.goos = "darwin"
	profile := filepath.Join("/Library/Managed Preferences", "com.anthropic.claudecode.plist")
	perUser := filepath.Join("/Library/Managed Preferences", "renan", "com.anthropic.claudecode.plist")
	files[profile], files[perUser] = true, true
	m.registry = func() []string { return []string{`HKLM\SOFTWARE\Policies\ClaudeCode`} }
	want = []string{profile, perUser, `HKLM\SOFTWARE\Policies\ClaudeCode`}
	slices.Sort(want)
	if got := managedSources(m); !slices.Equal(got, want) {
		t.Errorf("darwin:\ngot  %q\nwant %q", got, want)
	}

	m.goos, m.getenv = "windows", func(string) string { return `D:\Apps` }
	if got := systemDir(m); got != filepath.Join(`D:\Apps`, "ClaudeCode") {
		t.Errorf("windows system dir = %q", got)
	}
}

// runtimes are the preludes and the commands that run them. Tests skip a
// runtime that isn't installed.
type preludeRuntime struct {
	name    string
	prelude []byte
	file    string
	harness string
	command []string
}

const nodeHarness = `import fs from "node:fs";
import { pathToFileURL } from "node:url";
const [preludePath, mode, input, cacheDir] = process.argv.slice(2);
const { isolate, adopt, parseDotenv } = await import(pathToFileURL(preludePath).href);
if (mode === "dotenv") {
  console.log(JSON.stringify(parseDotenv(fs.readFileSync(input, "utf8"))));
} else {
  try {
    const { env, root, cleanup } = isolate(JSON.parse(fs.readFileSync(input, "utf8")), { cacheDir });
    adopt(env);
    const seen = { ...process.env };
    const existed = fs.existsSync(root);
    cleanup();
    console.log(JSON.stringify({ env: seen, root, existed, removed: !fs.existsSync(root) }));
  } catch (err) {
    console.log(JSON.stringify({ error: err.message }));
  }
}
`

const pythonHarness = `import json, os, sys
prelude_path, mode, input_path = sys.argv[1:4]
sys.path.insert(0, os.path.dirname(prelude_path))
import isolate as prelude
if mode == "dotenv":
    with open(input_path, encoding="utf-8") as f:
        print(json.dumps(prelude.parse_dotenv(f.read())))
else:
    try:
        with open(input_path, encoding="utf-8") as f:
            spec = json.load(f)
        env, root, cleanup = prelude.isolate(spec, sys.argv[4])
        prelude.adopt(env)
        seen = dict(os.environ)
        existed = os.path.exists(root)
        cleanup()
        print(json.dumps({"env": seen, "root": root, "existed": existed, "removed": not os.path.exists(root)}))
    except Exception as err:
        print(json.dumps({"error": str(err)}))
`

func runtimes(t *testing.T) []preludeRuntime {
	t.Helper()
	var out []preludeRuntime
	if node := works("node"); node != "" {
		out = append(out, preludeRuntime{"node", PreludeJS, "isolate.mjs", nodeHarness, []string{node, "harness"}})
	}
	pythons := []string{"python3", "python"}
	if runtime.GOOS == "windows" {
		// python3 on Windows is often the Microsoft Store stub.
		pythons = []string{"python"}
	}
	for _, name := range pythons {
		if py := works(name); py != "" {
			out = append(out, preludeRuntime{"python", PreludePython, "isolate.py", pythonHarness, []string{py, "-I", "harness"}})
			break
		}
	}
	if len(out) == 0 {
		t.Skip("neither node nor python is installed")
	}
	return out
}

// works returns the path of a runtime that is installed and runs, or "".
func works(name string) string {
	path, err := exec.LookPath(name)
	if err != nil || exec.Command(path, "--version").Run() != nil {
		return ""
	}
	return path
}

// run runs a prelude's harness with exactly the given environment and
// returns its JSON output.
func (r preludeRuntime) run(t *testing.T, env []string, args ...string) map[string]any {
	t.Helper()
	dir := t.TempDir()
	prelude := filepath.Join(dir, r.file)
	harness := filepath.Join(dir, "harness"+filepath.Ext(r.file))
	if err := os.WriteFile(prelude, r.prelude, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(harness, []byte(r.harness), 0o644); err != nil {
		t.Fatal(err)
	}
	argv := slices.Clone(r.command)
	argv[len(argv)-1] = harness
	cmd := exec.Command(argv[0], append(argv[1:], append([]string{prelude}, args...)...)...)
	cmd.Env = env
	out, err := cmd.Output()
	if err != nil {
		var stderr []byte
		if ee := (*exec.ExitError)(nil); errors.As(err, &ee) {
			stderr = ee.Stderr
		}
		t.Fatalf("%s harness: %v\n%s", r.name, err, stderr)
	}
	var res map[string]any
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatalf("%s harness output %q: %v", r.name, out, err)
	}
	return res
}

func TestPreludeIsolates(t *testing.T) {
	work := t.TempDir()
	bin := filepath.Join(work, "bin")
	other := filepath.Join(work, "other")
	tmp := filepath.Join(work, "tmp")
	cache := filepath.Join(work, "cache")
	for _, d := range []string{bin, other, tmp} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	npx := filepath.Join(bin, "npx")
	if runtime.GOOS == "windows" {
		npx += ".cmd"
	}
	if err := os.WriteFile(npx, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "secrets.env"), []byte("MCP_TOKEN=from-dotenv\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	iso := For(agent(work), needs)
	extra := filepath.Join(work, "image-bin")
	iso.Path = []string{extra}
	specPath := filepath.Join(work, "isolation.json")
	if err := os.WriteFile(specPath, iso.JSON(), 0o644); err != nil {
		t.Fatal(err)
	}

	// The caller's environment, with leaks a run must not see.
	caller := []string{
		"PATH=" + bin + string(os.PathListSeparator) + other,
		"TMPDIR=" + tmp, "TEMP=" + tmp, "TMP=" + tmp,
		"HOME=" + filepath.Join(work, "real-home"),
		"LANG=C.UTF-8",
		"ANTHROPIC_API_KEY=test-key",
		"ANTHROPIC_BASE_URL=http://leak.example",
		"CLAUDE_CODE_USE_BEDROCK=1",
		"CLAUDE_CONFIG_DIR=" + filepath.Join(work, "real-config"),
		"MCP_TOKEN=from-caller",
		"LEAK_ME=1",
	}
	for _, name := range []string{"SYSTEMROOT", "PATHEXT"} {
		if v := os.Getenv(name); v != "" {
			caller = append(caller, name+"="+v)
		}
	}

	allowed := map[string]bool{"PATH": true}
	for _, list := range [][]string{iso.Pass, iso.Secrets, mapKeys(iso.Set), mapKeys(iso.Scratch), mapKeys(iso.Cache)} {
		for _, k := range list {
			allowed[strings.ToUpper(k)] = true
		}
	}

	for _, r := range runtimes(t) {
		t.Run(r.name, func(t *testing.T) {
			res := r.run(t, caller, "isolate", specPath, cache)
			if msg, ok := res["error"]; ok {
				t.Fatalf("isolate failed: %v", msg)
			}
			env := map[string]string{}
			for k, v := range res["env"].(map[string]any) {
				env[strings.ToUpper(k)] = v.(string)
				if !allowed[strings.ToUpper(k)] {
					t.Errorf("%s reached the run", k)
				}
			}
			root := res["root"].(string)
			for name, want := range map[string]string{
				"ANTHROPIC_API_KEY":               "test-key",
				"LANG":                            "C.UTF-8",
				"MCP_TOKEN":                       "from-dotenv",
				"CLAUDE_CODE_DISABLE_AUTO_MEMORY": "1",
				"HOME":                            filepath.Join(root, "home"),
				"CLAUDE_CONFIG_DIR":               filepath.Join(root, "home", ".claude"),
				"NPM_CONFIG_CACHE":                filepath.Join(cache, "npm"),
			} {
				if got := env[name]; !samePath(got, want) {
					t.Errorf("%s = %q, want %q", name, got, want)
				}
			}
			if !strings.HasPrefix(root, tmp) {
				t.Errorf("scratch %s isn't under the caller's temp directory %s", root, tmp)
			}
			path := filepath.SplitList(env["PATH"])
			if !slices.Contains(path, bin) || !slices.Contains(path, extra) || slices.Contains(path, other) {
				t.Errorf("PATH = %q: want npx's folder and the spec's path, and not the caller's other folders", env["PATH"])
			}
			if res["existed"] != true || res["removed"] != true {
				t.Errorf("scratch directory: existed %v, removed after cleanup %v", res["existed"], res["removed"])
			}
		})
	}

	// A missing binary or secret fails before the run starts.
	missing := iso
	missing.Binaries = []string{"not-a-real-binary"}
	missingPath := filepath.Join(work, "missing-binary.json")
	if err := os.WriteFile(missingPath, missing.JSON(), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "secrets.env"), []byte("OTHER=1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, r := range runtimes(t) {
		t.Run(r.name+"/missing", func(t *testing.T) {
			res := r.run(t, caller, "isolate", missingPath, cache)
			if res["error"] != "not found on PATH: not-a-real-binary" {
				t.Errorf("missing binary: %v", res)
			}
			res = r.run(t, caller, "isolate", specPath, cache)
			if want := "secrets not set in " + iso.Dotenv + ": MCP_TOKEN"; res["error"] != want {
				t.Errorf("missing secret: got %v, want error %q", res, want)
			}
		})
	}
}

// TestPreludeRenamesVaultSecret checks that a Vault credential for a
// variable sets only that variable, from the secret it names, and that the
// secret doesn't also reach the run under its own name.
func TestPreludeRenamesVaultSecret(t *testing.T) {
	work := t.TempDir()
	a := agent(work)
	a.Definition.Vaults = map[string]resolve.Vault{"tracker": {Credentials: []resolve.Credential{
		{Name: "gh", SecretRef: "GITHUB_PAT", Env: "GH_TOKEN"},
		{Name: "tracker", SecretRef: "MCP_TOKEN", MCPServer: "tracker"},
	}}}
	iso := For(a, Needs{})
	if !slices.Equal(iso.Secrets, []string{"MCP_TOKEN"}) || !reflect.DeepEqual(iso.SecretEnv, map[string]string{"GH_TOKEN": "GITHUB_PAT"}) {
		t.Fatalf("For: Secrets %v, SecretEnv %v", iso.Secrets, iso.SecretEnv)
	}
	specPath := filepath.Join(work, "isolation.json")
	if err := os.WriteFile(specPath, iso.JSON(), 0o644); err != nil {
		t.Fatal(err)
	}
	dotenv := filepath.Join(work, "secrets.env")
	if err := os.WriteFile(dotenv, []byte("MCP_TOKEN=m\nGITHUB_PAT=g\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	caller := []string{"PATH=" + os.Getenv("PATH"), "TMPDIR=" + t.TempDir(), "GITHUB_PAT=from-caller"}
	for _, name := range []string{"SYSTEMROOT", "PATHEXT", "TEMP", "TMP"} {
		if v := os.Getenv(name); v != "" {
			caller = append(caller, name+"="+v)
		}
	}
	cache := filepath.Join(work, "cache")
	for _, r := range runtimes(t) {
		t.Run(r.name, func(t *testing.T) {
			res := r.run(t, caller, "isolate", specPath, cache)
			if msg, ok := res["error"]; ok {
				t.Fatalf("isolate failed: %v", msg)
			}
			env := map[string]string{}
			for k, v := range res["env"].(map[string]any) {
				env[strings.ToUpper(k)] = v.(string)
			}
			if env["GH_TOKEN"] != "g" || env["MCP_TOKEN"] != "m" {
				t.Errorf("GH_TOKEN = %q, MCP_TOKEN = %q; want g and m", env["GH_TOKEN"], env["MCP_TOKEN"])
			}
			if v, ok := env["GITHUB_PAT"]; ok {
				t.Errorf("GITHUB_PAT reached the run as %q; only GH_TOKEN should", v)
			}
		})
	}

	// A renamed credential's secret must be set like any other.
	if err := os.WriteFile(dotenv, []byte("MCP_TOKEN=m\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, r := range runtimes(t) {
		t.Run(r.name+"/missing", func(t *testing.T) {
			res := r.run(t, caller, "isolate", specPath, cache)
			if want := "secrets not set in " + iso.Dotenv + ": GITHUB_PAT"; res["error"] != want {
				t.Errorf("got %v, want error %q", res, want)
			}
		})
	}
}

func samePath(a, b string) bool {
	if a == b {
		return true
	}
	// macOS temp directories are reached through /private.
	ea, err1 := filepath.EvalSymlinks(a)
	eb, err2 := filepath.EvalSymlinks(b)
	return err1 == nil && err2 == nil && ea == eb
}

func mapKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// TestPreludeDotenvMatchesGo checks that the preludes read a dotenv file as
// Solaria's own parser does.
func TestPreludeDotenvMatchesGo(t *testing.T) {
	text := "# comment\n" +
		"export A=1\n" +
		"B = two words # trailing comment\n" +
		"C='single # kept'\n" +
		`D="line\nbreak \"quoted\""` + "\n" +
		"E=\n" +
		"F=x#y\n" +
		"\n   G =  spaced  \n"
	want, err := secrets.ParseDotenv([]byte(text))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "sample.env")
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	env := []string{}
	if v := os.Getenv("SYSTEMROOT"); v != "" {
		env = append(env, "SYSTEMROOT="+v)
	}
	for _, r := range runtimes(t) {
		t.Run(r.name, func(t *testing.T) {
			res := r.run(t, env, "dotenv", path)
			got := map[string]string{}
			for k, v := range res {
				got[k] = v.(string)
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("got  %q\nwant %q", got, want)
			}
		})
	}
}
