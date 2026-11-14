package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

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
