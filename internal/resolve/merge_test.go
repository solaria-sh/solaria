package resolve

import (
	"encoding/json"
	"testing"
)

func TestMergePatch(t *testing.T) {
	target := map[string]any{"a": 1, "b": map[string]any{"c": 2, "d": 3}}
	patch := map[string]any{"a": nil, "b": map[string]any{"c": 9}, "e": []any{1}}
	got := mergePatch(target, patch)
	want := map[string]any{"b": map[string]any{"c": 9, "d": 3}, "e": []any{1}}
	gb, _ := json.Marshal(got)
	wb, _ := json.Marshal(want)
	if string(gb) != string(wb) {
		t.Errorf("mergePatch = %s, want %s", gb, wb)
	}
	if _, ok := target["a"]; !ok || target["b"].(map[string]any)["c"] != 2 {
		t.Errorf("mergePatch modified its target: %v", target)
	}
}
