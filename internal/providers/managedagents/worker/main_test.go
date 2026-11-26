package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go/v2"
	"github.com/anthropics/anthropic-sdk-go/v2/tools/agenttoolset"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// The test binary doubles as the stdio MCP server the tests run, so they
// need neither npm, PyPI nor the network.
func TestMain(m *testing.M) {
	if os.Getenv("SOLARIA_TEST_MCP_SERVER") == "1" {
		serve()
		return
	}
	os.Exit(m.Run())
}

type echoInput struct {
	Text string `json:"text" jsonschema:"what to echo"`
}

// serve is an MCP server with one tool, echo, which also reports what the
// worker let it see: its arguments, workdir and variables.
func serve() {
	s := mcpsdk.NewServer(&mcpsdk.Implementation{Name: "echo", Version: "1"}, nil)
	mcpsdk.AddTool(s, &mcpsdk.Tool{Name: "echo", Description: "Echo the text."},
		func(_ context.Context, _ *mcpsdk.CallToolRequest, in echoInput) (*mcpsdk.CallToolResult, any, error) {
			wd, _ := os.Getwd()
			text := fmt.Sprintf("%s args=%v wd=%s token=%s key=%q",
				in.Text, os.Args[1:], filepath.Base(wd), os.Getenv("ECHO_TOKEN"), os.Getenv("ANTHROPIC_ENVIRONMENT_KEY"))
			return &mcpsdk.CallToolResult{Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: text}}}, nil, nil
		})
	if err := s.Run(context.Background(), &mcpsdk.StdioTransport{}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// echoServer installs the test binary as a PyPI package's command.
func echoServer(t *testing.T) server {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(exe, filepath.Join(dir, "bin", "echo-mcp")); err != nil {
		t.Fatal(err)
	}
	return server{
		Name: "echo", Runner: "pypi", Dir: dir, Package: "echo-mcp",
		Args:      []string{"--root", "/workspace"},
		Env:       map[string]string{"SOLARIA_TEST_MCP_SERVER": "1"},
		SecretEnv: map[string]string{"ECHO_TOKEN": "SOLARIA_TEST_ECHO_TOKEN"},
	}
}

// TestWrappedTool lists a server's tools and answers a custom tool call
// the way a session does, then ends the session.
func TestWrappedTool(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the worker runs in Linux containers, and starts a server's command by a name without .exe")
	}
	t.Setenv("SOLARIA_TEST_ECHO_TOKEN", "echo-secret")
	t.Setenv("ANTHROPIC_ENVIRONMENT_KEY", "envkey-secret")
	s := echoServer(t)
	ctx := context.Background()
	workdir := filepath.Join(t.TempDir(), "session")
	if err := os.Mkdir(workdir, 0o755); err != nil {
		t.Fatal(err)
	}

	tools, err := s.list(ctx, workdir)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 1 || tools[0].Name != "echo" {
		t.Fatalf("listed %v, want echo", tools)
	}

	ss := &sessionServers{workdir: workdir}
	w := newWrappedTool(ss, s, tools[0])
	if w.Name() != "echo__echo" || w.Description() != "Echo the text." {
		t.Errorf("declared as %s: %q", w.Name(), w.Description())
	}
	if _, ok := w.InputSchema().Properties.(map[string]any)["text"]; !ok {
		t.Errorf("input schema lacks text: %+v", w.InputSchema())
	}
	out, err := w.Execute(ctx, json.RawMessage(`{"text":"hi"}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || out[0].OfText == nil {
		t.Fatalf("result: %+v", out)
	}
	got := out[0].OfText.Text
	want := `hi args=[--root /workspace] wd=session token=echo-secret key=""`
	if got != want {
		t.Errorf("result %q, want %q", got, want)
	}

	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Execute(ctx, json.RawMessage(`{"text":"hi"}`)); err == nil || !strings.Contains(err.Error(), "ended") {
		t.Errorf("call after the session ended: err = %v", err)
	}
}

func TestNpmBin(t *testing.T) {
	for _, tc := range []struct {
		name, bin, want, err string
	}{
		{name: "@scope/files-mcp", bin: `"cli.js"`, want: "files-mcp"},
		{name: "files", bin: `{"files-server": "a.js"}`, want: "files-server"},
		{name: "@scope/files", bin: `{"files": "a.js", "other": "b.js"}`, want: "files"},
		{name: "files", bin: `{"a": "a.js", "b": "b.js"}`, err: "installs 2 commands and none is named files"},
		{name: "files", bin: `{}`, err: "installs no command"},
	} {
		dir := t.TempDir()
		pkgDir := filepath.Join(dir, "node_modules", tc.name)
		if err := os.MkdirAll(pkgDir, 0o755); err != nil {
			t.Fatal(err)
		}
		pj := fmt.Sprintf(`{"name": %q, "bin": %s}`, tc.name, tc.bin)
		if err := os.WriteFile(filepath.Join(pkgDir, "package.json"), []byte(pj), 0o644); err != nil {
			t.Fatal(err)
		}
		got, err := npmBin(dir, tc.name)
		if tc.err != "" {
			if err == nil || !strings.Contains(err.Error(), tc.err) {
				t.Errorf("%s %s: err = %v, want %q", tc.name, tc.bin, err, tc.err)
			}
			continue
		}
		if err != nil || got != tc.want {
			t.Errorf("%s %s: %q, %v, want %q", tc.name, tc.bin, got, err, tc.want)
		}
	}
}

// TestBashEnv checks that the agent's bash sees the container's ordinary
// variables, but neither the worker's key nor the MCP servers' secrets.
func TestBashEnv(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the worker's bash runs in Linux containers")
	}
	cfg := &config{Servers: []server{{Name: "files", SecretEnv: map[string]string{"FILES_TOKEN": "FILES_TOKEN"}}}}
	environ := []string{"PATH=" + os.Getenv("PATH"), "HOME=/root", "GOPATH=/opt/go", "FILES_TOKEN=files-secret", "ANTHROPIC_ENVIRONMENT_KEY=envkey-secret"}
	env := &agenttoolset.AgentToolContext{Workdir: t.TempDir(), Env: bashEnv(environ, cfg.secretVars())}
	tools := agenttoolset.BetaAgentToolset20260401(env)
	defer agenttoolset.CloseAll(tools)
	var bash anthropic.BetaTool
	for _, tool := range tools {
		if tool.Name() == "bash" {
			bash = tool
		}
	}
	if bash == nil {
		t.Fatal("no bash tool in the toolset")
	}
	out, err := bash.Execute(context.Background(), json.RawMessage(`{"command": "env"}`))
	if err != nil {
		t.Fatal(err)
	}
	var text strings.Builder
	for _, b := range out {
		if b.OfText != nil {
			text.WriteString(b.OfText.Text)
		}
	}
	got := text.String()
	if !strings.Contains(got, "GOPATH=/opt/go") {
		t.Errorf("bash lacks the container's variables:\n%s", got)
	}
	for _, secret := range []string{"files-secret", "envkey-secret"} {
		if strings.Contains(got, secret) {
			t.Errorf("bash sees %s:\n%s", secret, got)
		}
	}
}

// TestSetup runs setup steps in a workdir: in order, each with its own
// variables and its secret read from the container's, never the worker's
// key. A failing step makes every tool of the session return its error.
func TestSetup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("setup steps run with sh in Linux containers")
	}
	t.Setenv("ANTHROPIC_ENVIRONMENT_KEY", "envkey-secret")
	t.Setenv("SOLARIA_TEST_STEP_SECRET", "step-secret")
	dir := t.TempDir()
	steps := []step{
		{Name: "first", Run: "echo one >> order; env > first.env", Env: map[string]string{"GREETING": "hi"}, SecretEnv: map[string]string{"TOKEN": "SOLARIA_TEST_STEP_SECRET"}},
		{Name: "second", Run: "echo two >> order"},
	}
	log, err := os.CreateTemp(t.TempDir(), "log")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = log.Close() }()
	if err := runSetup(context.Background(), steps, dir, log); err != nil {
		t.Fatal(err)
	}
	order, _ := os.ReadFile(filepath.Join(dir, "order"))
	if string(order) != "one\ntwo\n" {
		t.Errorf("steps ran as %q", order)
	}
	env, _ := os.ReadFile(filepath.Join(dir, "first.env"))
	if !strings.Contains(string(env), "GREETING=hi") || !strings.Contains(string(env), "TOKEN=step-secret") {
		t.Errorf("the first step's environment lacks its variables:\n%s", env)
	}
	if strings.Contains(string(env), "envkey-secret") || strings.Contains(string(env), "SOLARIA_TEST_STEP_SECRET") {
		t.Errorf("a step sees the worker's environment:\n%s", env)
	}

	err = runSetup(context.Background(), []step{{Name: "broken", Run: "exit 3"}, {Name: "after", Run: "touch after"}}, dir, log)
	if err == nil || err.Error() != "setup step broken exited with 3" {
		t.Fatalf("a failing step gave %v", err)
	}
	if _, serr := os.Stat(filepath.Join(dir, "after")); serr == nil {
		t.Error("a step ran after one failed")
	}
	tools := failAll(agenttoolset.BetaAgentToolset20260401(&agenttoolset.AgentToolContext{Workdir: dir}), err)
	defer agenttoolset.CloseAll(tools)
	for _, tool := range tools {
		if _, xerr := tool.Execute(context.Background(), json.RawMessage(`{"command": "ls"}`)); xerr == nil || !strings.Contains(xerr.Error(), "setup step broken exited with 3") {
			t.Errorf("%s after a failed setup: err = %v", tool.Name(), xerr)
		}
	}

	cfg := &config{Setup: steps}
	if !slices.Contains(cfg.secretVars(), "SOLARIA_TEST_STEP_SECRET") {
		t.Errorf("secretVars() = %v lacks the step's secret, so bash would see it", cfg.secretVars())
	}
}
