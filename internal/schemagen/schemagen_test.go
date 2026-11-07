package schemagen

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/solaria-sh/solaria/internal/api/v1alpha1"
	"github.com/solaria-sh/solaria/schemas"
)

// TestCommittedSchemasAreCurrent fails when the Go types changed but the
// committed schema files weren't regenerated.
func TestCommittedSchemasAreCurrent(t *testing.T) {
	t.Chdir("../..") // Generate reads doc comments relative to the module root
	files, err := Generate()
	if err != nil {
		t.Fatal(err)
	}
	committed, err := fs.Glob(schemas.V1alpha1(), "*.json")
	if err != nil {
		t.Fatal(err)
	}
	var generated []string
	for name, want := range files {
		generated = append(generated, name)
		got, err := fs.ReadFile(schemas.V1alpha1(), name)
		if err != nil || string(got) != string(want) {
			t.Errorf("%s/%s is out of date; run make schema", OutDir, name)
		}
	}
	slices.Sort(generated)
	if !slices.Equal(generated, committed) {
		t.Errorf("committed schema files %v, generated %v; run make schema and delete stale files", committed, generated)
	}
}

// TestCommittedReferenceIsCurrent fails when the card reference wasn't
// regenerated with the schema.
func TestCommittedReferenceIsCurrent(t *testing.T) {
	t.Chdir("../..")
	want, err := Reference()
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.FromSlash(ReferenceFile))
	if err != nil || string(got) != string(want) {
		t.Errorf("%s is out of date; run make schema", ReferenceFile)
	}
}

// TestEveryPropertyIsDescribed keeps the schema useful in editors: every
// property needs a doc comment on its Go field.
func TestEveryPropertyIsDescribed(t *testing.T) {
	t.Chdir("../..")
	files, err := Generate()
	if err != nil {
		t.Fatal(err)
	}
	for name, b := range files {
		var doc any
		if err := json.Unmarshal(b, &doc); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		checkDescribed(t, name, "#", doc)
	}
}

func checkDescribed(t *testing.T, file, at string, v any) {
	t.Helper()
	switch v := v.(type) {
	case map[string]any:
		if props, ok := v["properties"].(map[string]any); ok {
			for p, s := range props {
				m, _ := s.(map[string]any)
				if d, _ := m["description"].(string); d == "" {
					t.Errorf("%s %s/properties/%s has no description", file, at, p)
				}
			}
		}
		for k, child := range v {
			if k == "if" {
				continue // conditions restate properties; they aren't docs
			}
			checkDescribed(t, file, at+"/"+k, child)
		}
	case []any:
		for _, child := range v {
			checkDescribed(t, file, at, child)
		}
	}
}

func TestTidyComment(t *testing.T) {
	const pkg = modulePath + "/" + apiDir
	tests := []struct{ key, text, want string }{
		{pkg + ".AgentSpec", "AgentSpec describes an agent\nindependently.", "Describes an agent independently."},
		{pkg + ".KeyValue", "KeyValue is a named value.", "A named value."},
		{pkg + ".AgentSpec.Model", "The model\nthe agent uses.", "The model the agent uses."},
		{pkg + ".Agent", "Declares what an agent is.", "Declares what an agent is."},
	}
	for _, tt := range tests {
		if got := tidyComment(tt.key, tt.text); got != tt.want {
			t.Errorf("tidyComment(%q, %q) = %q, want %q", tt.key, tt.text, got, tt.want)
		}
	}
}

// TestCombinedSchemaStandsAlone checks that editors can use solaria.json on
// its own: it compiles with no other schema available and dispatches on
// kind.
func TestCombinedSchemaStandsAlone(t *testing.T) {
	doc, err := jsonschema.UnmarshalJSON(mustOpen(t, schemas.CombinedFile))
	if err != nil {
		t.Fatal(err)
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource(schemas.BaseURL+schemas.CombinedFile, doc); err != nil {
		t.Fatal(err)
	}
	sch, err := c.Compile(schemas.BaseURL + schemas.CombinedFile)
	if err != nil {
		t.Fatalf("compile %s alone: %v", schemas.CombinedFile, err)
	}
	agent := map[string]any{
		"apiVersion": v1alpha1.APIVersion,
		"kind":       "Agent",
		"metadata":   map[string]any{"name": "reviewer"},
		"spec": map[string]any{
			"instructions": []any{"role.md"},
			"model":        map[string]any{"preferred": "anthropic:claude-sonnet-5-5"},
		},
	}
	if err := sch.Validate(agent); err != nil {
		t.Errorf("valid agent rejected: %v", err)
	}
	agent["kind"] = "McpServer"
	if err := sch.Validate(agent); err == nil {
		t.Error("an Agent's spec validated as an McpServer; dispatch on kind is broken")
	}
}

func mustOpen(t *testing.T, name string) fs.File {
	t.Helper()
	f, err := schemas.V1alpha1().Open(name)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return f
}
