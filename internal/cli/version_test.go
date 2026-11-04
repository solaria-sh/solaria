package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestVersionCommand(t *testing.T) {
	var out bytes.Buffer
	root := NewRootCommand()
	root.SetOut(&out)
	root.SetArgs([]string{"version"})

	if err := root.Execute(); err != nil {
		t.Fatalf("version: %v", err)
	}
	if got := out.String(); !strings.HasPrefix(got, "solaria dev ") {
		t.Errorf("version output = %q, want prefix %q", got, "solaria dev ")
	}
}

func TestVersionCommandRejectsArgs(t *testing.T) {
	root := NewRootCommand()
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{"version", "extra"})

	if err := root.Execute(); err == nil {
		t.Fatal("version with an extra argument: want error, got nil")
	}
}
