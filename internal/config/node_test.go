package config

import (
	"reflect"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func parse(t *testing.T, src string) *yaml.Node {
	t.Helper()
	var n yaml.Node
	if err := yaml.Unmarshal([]byte(src), &n); err != nil {
		t.Fatal(err)
	}
	return &n
}

func TestToValue(t *testing.T) {
	n := parse(t, `
str: hello
int: 42
hex: 0x1f
float: 1.5
bool: true
null: ~
1: numeric key
date: 2026-01-01
version: 1.10
list: [a, 1]
anchor: &a {x: 1}
alias: *a
`)
	got, diags := toValue(n, "f.yaml")
	if len(diags) > 0 {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	want := map[string]any{
		"str":     "hello",
		"int":     int64(42),
		"hex":     int64(31),
		"float":   1.5,
		"bool":    true,
		"null":    nil,
		"1":       "numeric key",
		"date":    "2026-01-01", // dates stay strings
		"version": 1.1,          // an unquoted version is a number, as in JSON
		"list":    []any{"a", int64(1)},
		"anchor":  map[string]any{"x": int64(1)},
		"alias":   map[string]any{"x": int64(1)},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("toValue =\n%#v\nwant\n%#v", got, want)
	}
}

func TestToValueRejects(t *testing.T) {
	tests := []struct{ name, src, want string }{
		{"duplicate key", "a: 1\na: 2\n", `2:1: error: duplicate key "a" (first at line 1)`},
		{"merge key", "base: &b {x: 1}\nc:\n  <<: *b\n", "3:3: error: merge keys (<<) aren't supported"},
		{"sequence key", "? [a]\n: one\n", "1:3: error: keys must be scalars"},
		{"infinity", "a: .inf\n", "1:4: error: number .inf can't be represented in JSON"},
		{"custom tag", "a: !secret x\n", "1:4: error: unsupported YAML tag !secret"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, diags := toValue(parse(t, tt.src), "f.yaml")
			if len(diags) != 1 || !strings.HasPrefix(diags[0].String(), "f.yaml:"+tt.want) {
				t.Errorf("diagnostics = %v, want one starting with f.yaml:%s", diags, tt.want)
			}
		})
	}
}

func TestLocateAndFieldPath(t *testing.T) {
	n := parse(t, `spec:
  tools:
    mcp:
      - ref: a
      - ref: b
`)
	key, val, ok := locate(n, []string{"spec", "tools", "mcp", "1", "ref"})
	if !ok || val.Value != "b" || val.Line != 5 || key == nil || key.Value != "ref" {
		t.Errorf("locate = (%v, %v, %v), want the ref: b node on line 5", key, val, ok)
	}
	_, val, ok = locate(n, []string{"spec", "tools", "missing"})
	if ok || val.Line != 3 {
		t.Errorf("locate of a missing field = (line %d, %v), want the deepest found node (line 3) and false", val.Line, ok)
	}
	if got, want := fieldPath(n, []string{"spec", "tools", "mcp", "1", "ref"}), "spec.tools.mcp[1].ref"; got != want {
		t.Errorf("fieldPath = %q, want %q", got, want)
	}
}

func TestSuggest(t *testing.T) {
	allowed := []string{"instructions", "limits", "model", "skills"}
	for got, want := range map[string]string{
		"limts":        "limits",
		"Model":        "model",
		"instruction":  "instructions",
		"permissionss": "",
	} {
		if s := suggest(got, allowed); s != want {
			t.Errorf("suggest(%q) = %q, want %q", got, s, want)
		}
	}
}
