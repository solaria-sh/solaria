package process

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/solaria-sh/solaria/internal/api/v1alpha1"
	"github.com/solaria-sh/solaria/internal/resolve"
)

// TestSRTIntegration runs a shell inside the real srt with the settings
// Solaria generates (ADR 0017), and checks what it may write and reach:
// the workspace, the cache and srt's temp folder are writable, other
// folders aren't; the model API is reachable, other hosts aren't. It needs
// srt and curl, and the network, so it only runs with SOLARIA_IT_SRT=1.
func TestSRTIntegration(t *testing.T) {
	if os.Getenv("SOLARIA_IT_SRT") != "1" {
		t.Skip("set SOLARIA_IT_SRT=1, with srt and curl on PATH, to run a shell inside srt")
	}
	for _, p := range []string{SRT, "curl"} {
		if _, err := exec.LookPath(p); err != nil {
			t.Fatalf("SOLARIA_IT_SRT needs %s on PATH", p)
		}
	}
	root := t.TempDir()
	workspace, cache, outside := filepath.Join(root, "workspace"), filepath.Join(root, "cache"), filepath.Join(root, "outside")
	for _, d := range []string{workspace, cache, outside} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	a := &resolve.Agent{Name: "it", Env: &v1alpha1.TargetSpec{}}
	settings := filepath.Join(root, SRTSettingsFile)
	if err := os.WriteFile(settings, SRTFor(a, cache, []string{"api.anthropic.com"}).JSON(), 0o644); err != nil {
		t.Fatal(err)
	}
	script := `
w() { if (mkdir -p "$2" && touch "$2/f") 2>/dev/null; then echo "$1 writable"; else echo "$1 read-only"; fi; }
w workspace ./made
w cache "$CACHE/made"
w tmp "$TMPDIR/made"
w outside "$OUTSIDE/made"
r() { if curl -s -o /dev/null --max-time 20 "$2"; then echo "$1 reachable"; else echo "$1 blocked"; fi; }
r model https://api.anthropic.com/
r other https://example.com/
`
	cmd := exec.Command(SRT, SRTArgv(settings, []string{"sh", "-c", script})[1:]...)
	cmd.Dir = workspace
	cmd.Env = append(os.Environ(), "CACHE="+cache, "OUTSIDE="+outside)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("srt: %v\n%s", err, out)
	}
	for _, want := range []string{
		"workspace writable", "cache writable", "tmp writable", "outside read-only",
		"model reachable", "other blocked",
	} {
		if !strings.Contains(string(out), want+"\n") {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}
