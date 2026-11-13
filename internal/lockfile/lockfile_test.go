package lockfile

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const digest = "sha256:0000000000000000000000000000000000000000000000000000000000000000"

// TestReadV1 reads a lock file written before ADR 0020, which kept the
// targets under environments, and checks Write stores it as v2.
func TestReadV1(t *testing.T) {
	path := filepath.Join(t.TempDir(), "solaria.lock")
	v1 := `{
  "format": "solaria.sh/lock/v1",
  "definitionFormat": "solaria.sh/definition/v1",
  "environments": {"prod": {"agents": {"Agent/triage": {"specDigest": "` + digest + `", "resolvedDigest": "` + digest + `"}}}}
}
`
	if err := os.WriteFile(path, []byte(v1), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := Read(path)
	if err != nil {
		t.Fatal(err)
	}
	if f.Format != Format || f.Targets["prod"] == nil || f.Targets["prod"].Agents["Agent/triage"].ResolvedDigest != digest {
		t.Fatalf("Read of a v1 lock = format %q, targets %v", f.Format, f.Targets)
	}
	if err := Write(path, f); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(raw, []byte(`"format": "solaria.sh/lock/v2"`)) || !bytes.Contains(raw, []byte(`"targets": {`)) || bytes.Contains(raw, []byte("environments")) {
		t.Errorf("lock after writing a v1 file:\n%s", raw)
	}
}

// TestReadMixedNames refuses a file that uses the other format's key.
func TestReadMixedNames(t *testing.T) {
	for name, body := range map[string]string{
		"v1 with targets":      `{"format": "solaria.sh/lock/v1", "definitionFormat": "solaria.sh/definition/v1", "targets": {}}`,
		"v2 with environments": `{"format": "solaria.sh/lock/v2", "definitionFormat": "solaria.sh/definition/v1", "environments": {}}`,
	} {
		path := filepath.Join(t.TempDir(), "solaria.lock")
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := Read(path); err == nil || !strings.Contains(err.Error(), "keeps targets under") {
			t.Errorf("%s: err = %v, want one naming where targets belong", name, err)
		}
	}
}
