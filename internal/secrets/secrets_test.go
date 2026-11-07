package secrets

import (
	"maps"
	"os"
	"path/filepath"
	"testing"

	"github.com/solaria-sh/solaria/internal/api/v1alpha1"
)

func TestParseDotenv(t *testing.T) {
	got, err := ParseDotenv([]byte(`# comment
A=plain
export B = spaced # trailing comment
C='single #not a comment'
D="tab\tand \"quote\""
E=
`))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"A": "plain", "B": "spaced", "C": "single #not a comment", "D": "tab\tand \"quote\"", "E": ""}
	if !maps.Equal(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
	if _, err := ParseDotenv([]byte("no equals sign\n")); err == nil {
		t.Error("a line without = parsed")
	}
}

func TestDotenvSource(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "secrets.env"), []byte("KEY=v\nEMPTY=\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := For(&v1alpha1.SecretsSpec{Source: v1alpha1.SecretsDotenv, Path: "secrets.env"}, dir)
	for name, want := range map[string]bool{"KEY": true, "EMPTY": false, "MISSING": false} {
		if _, ok, err := s.Lookup(name); err != nil || ok != want {
			t.Errorf("Lookup(%s) = %v, %v; want %v", name, ok, err, want)
		}
	}
	missing := For(&v1alpha1.SecretsSpec{Source: v1alpha1.SecretsDotenv}, dir)
	if _, ok, err := missing.Lookup("KEY"); ok || err != nil {
		t.Errorf("a missing .env gave %v, %v; want not found", ok, err)
	}
}

func TestEnvSource(t *testing.T) {
	t.Setenv("SOLARIA_TEST_SECRET", "v")
	if v, ok, _ := For(nil, "").Lookup("SOLARIA_TEST_SECRET"); !ok || v != "v" {
		t.Errorf("Lookup = %q, %v", v, ok)
	}
}
