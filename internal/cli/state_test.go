package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/solaria-sh/solaria/internal/config"
	"github.com/solaria-sh/solaria/internal/state"
)

func TestStateBackend(t *testing.T) {
	dir := t.TempDir()
	err := os.WriteFile(filepath.Join(dir, "envs.yaml"), []byte(`apiVersion: solaria.sh/v1alpha1
kind: Target
metadata: {name: shared}
spec:
  provider: stub
  placement: file
  state:
    azureBlob: {account: agentstate, container: solaria-state}
---
apiVersion: solaria.sh/v1alpha1
kind: Target
metadata: {name: local}
spec: {provider: stub, placement: file}
`), 0o644)
	if err != nil {
		t.Fatal(err)
	}
	set, diags := config.Load([]string{dir})
	if n, _ := diags.Count(); n > 0 {
		t.Fatal(diags)
	}
	for env, want := range map[string]string{"shared": "azure", "local": "local", "gone": "local"} {
		b, err := stateBackend(set, env, filepath.Join(dir, ".solaria", "state"))
		if err != nil {
			t.Fatal(err)
		}
		got := "local"
		if _, ok := b.(*state.AzureBlob); ok {
			got = "azure"
		}
		if got != want {
			t.Errorf("environment %s keeps state in %s, want %s", env, got, want)
		}
	}
}
