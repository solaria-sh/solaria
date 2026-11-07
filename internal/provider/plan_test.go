package provider

import (
	"reflect"
	"testing"

	"github.com/solaria-sh/solaria/internal/resolve"
)

func TestDiff(t *testing.T) {
	before := resolve.Definition{
		Format:       resolve.Format,
		Instructions: []string{"One.\n", "Two.\n"},
		Model:        resolve.Model{Preferred: "anthropic:a", Fallback: []string{}},
		Tools:        resolve.Tools{Builtin: []string{"read"}, MCP: map[string]resolve.MCPServer{}},
		Limits:       resolve.Limits{MaxTurns: 5},
		Providers:    map[string]resolve.ProviderFields{},
	}
	after := before
	after.Instructions = []string{"One, changed.\nAnd longer.\n"}
	after.Model.Preferred = "anthropic:b"
	after.Tools.Builtin = []string{"read", "shell"}
	after.Limits = resolve.Limits{}

	want := []Change{
		{Path: "instructions[0]", Old: "1 line", New: "2 lines", Text: true},
		{Path: "instructions[1]", Old: "1 line", New: "(none)", Text: true},
		{Path: "limits.maxTurns", Old: "5", New: "(none)"},
		{Path: "model.preferred", Old: `"anthropic:a"`, New: `"anthropic:b"`},
		{Path: "tools.builtin", Old: `["read"]`, New: `["read","shell"]`},
	}
	if got := Diff(&before, &after); !reflect.DeepEqual(got, want) {
		t.Errorf("Diff =\n%#v\nwant\n%#v", got, want)
	}
	if got := Diff(&before, &before); len(got) != 0 {
		t.Errorf("Diff of equal definitions = %v, want none", got)
	}
}

func TestShortDigest(t *testing.T) {
	if got := ShortDigest("sha256:0123456789abcdef"); got != "sha256:0123456789ab" {
		t.Errorf("ShortDigest = %q", got)
	}
}
