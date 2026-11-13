package gitagent

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/solaria-sh/solaria/internal/provider"
	"github.com/solaria-sh/solaria/internal/providers/container"
)

// TestDockerIntegration builds a real image, runs a scripted task in it and
// destroys it. It needs Docker and network access, and the first run takes
// several minutes, so it only runs with SOLARIA_IT_DOCKER=1. The scripted
// model needs no API key. SOLARIA_IT_PLATFORM picks the platform.
func TestDockerIntegration(t *testing.T) {
	if os.Getenv("SOLARIA_IT_DOCKER") != "1" {
		t.Skip("set SOLARIA_IT_DOCKER=1 to build and run a real image")
	}
	project := t.TempDir()
	write := func(dir, name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	platform := os.Getenv("SOLARIA_IT_PLATFORM")
	if platform == "" {
		platform = DefaultPlatform
	}
	write(project, "solaria.yaml", `apiVersion: solaria.sh/v1alpha1
kind: Agent
metadata: {name: it}
spec:
  instructions: [role.md]
  model: {preferred: "scripted:/workspace/script.yaml"}
  tools: {builtin: [shell]}
  limits: {maxTurns: 5, timeout: 2m}
---
apiVersion: solaria.sh/v1alpha1
kind: Target
metadata: {name: solaria-it}
spec:
  provider: gitagent
  placement: docker
  sandbox: {packages: {apt: [jq]}}
  providers:
    gitagent: {platform: `+platform+`}
`)
	write(project, "role.md", "You are Solaria's integration test agent.\n")

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Minute)
	defer cancel()
	p := NewWithDocker(provider.Options{Target: "solaria-it", WorkDir: project, Log: testLog{t}}, container.CLI{})
	a := load(t, project, "solaria-it")
	pl, err := p.Plan(ctx, a, nil)
	if err != nil {
		t.Fatal(err)
	}
	applied, err := p.Apply(ctx, pl)
	if err != nil {
		t.Fatal(err)
	}
	entry := entryFor(a, applied)
	t.Cleanup(func() {
		if err := p.Destroy(context.Background(), entry); err != nil {
			t.Errorf("destroy: %v", err)
		}
	})

	// gitagent accepts task_complete only after a command it counts as a
	// check (verify_patterns in its profile); cat isn't one, python3 -c is.
	work := t.TempDir()
	write(work, "script.yaml", `- tool_calls: [{name: exec, arguments: {cmd: 'jq -n "\"hello from solaria\"" > out.txt'}}]
- tool_calls: [{name: exec, arguments: {cmd: "python3 -c 'print(open(\"out.txt\").read())'"}}]
- tool_calls: [{name: task_complete, arguments: {summary: wrote out.txt}}]
`)
	run := applied.Binding.Outputs[outRun] + ` -p "Write out.txt."`
	cmd := exec.CommandContext(ctx, "sh", "-c", run)
	cmd.Dir = work
	out, err := cmd.CombinedOutput()
	t.Logf("%s\n%s", run, out)
	if err != nil {
		t.Fatalf("docker run: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(work, "out.txt"))
	if err != nil || strings.TrimSpace(string(got)) != `"hello from solaria"` {
		t.Errorf("out.txt = %q, %v", got, err)
	}

	obs, err := p.Read(ctx, entry)
	if err != nil || !obs.Exists || obs.RemoteHash != applied.RemoteHash {
		t.Errorf("Read = %+v, %v", obs, err)
	}
}

type testLog struct{ t *testing.T }

func (l testLog) Write(b []byte) (int, error) {
	l.t.Log(strings.TrimRight(string(b), "\n"))
	return len(b), nil
}
