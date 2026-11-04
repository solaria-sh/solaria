package digest

import (
	"strings"
	"testing"
)

func TestCanonical(t *testing.T) {
	tests := []struct {
		name string
		in   any
		want string
	}{
		{"keys sorted", map[string]any{"b": 1, "a": "x"}, `{"a":"x","b":1}`},
		{"nested", map[string]any{"z": []any{map[string]any{"y": true, "x": nil}}}, `{"z":[{"x":null,"y":true}]}`},
		{"numbers", []any{1.0, 1e21, 0.1}, `[1,1e+21,0.1]`},
		{"no HTML escaping", "<a & b>", `"<a & b>"`},
		{"unicode", "é", `"é"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Canonical(tt.in)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tt.want {
				t.Errorf("Canonical = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestOfIgnoresKeyOrder(t *testing.T) {
	type pair struct {
		B int    `json:"b"`
		A string `json:"a"`
	}
	a, err := Of(pair{B: 1, A: "x"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := Of(map[string]any{"a": "x", "b": 1})
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Errorf("struct and map with the same content: %s != %s", a, b)
	}
	if !strings.HasPrefix(a, Prefix) || len(a) != len(Prefix)+64 {
		t.Errorf("digest %q is not sha256:<64 hex>", a)
	}
}

func TestBytes(t *testing.T) {
	// SHA-256 of the empty string.
	const want = "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	if got := Bytes(nil); got != want {
		t.Errorf("Bytes(nil) = %s, want %s", got, want)
	}
}
