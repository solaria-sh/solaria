package resolve

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/solaria-sh/solaria/internal/api/v1alpha1"
)

// Output is what the agent's final answer must look like (ADR 0019).
type Output struct {
	// Schema is the parsed JSON Schema. Whether it was written inline or in
	// a JSON or YAML file doesn't change it, so it doesn't change the digest.
	Schema any `json:"schema"`
}

// OutputSchema reads an agent's output schema, from its file relative to
// dir or inline, and returns it as plain JSON values: maps, slices,
// strings, float64s, booleans and nil.
func OutputSchema(dir string, s v1alpha1.OutputSchema) (any, error) {
	if s.Inline != nil {
		return plainJSON(s.Inline)
	}
	b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(s.File)))
	if err != nil {
		return nil, err
	}
	var v any
	switch strings.ToLower(filepath.Ext(s.File)) {
	case ".json":
		dec := json.NewDecoder(bytes.NewReader(b))
		if err := dec.Decode(&v); err != nil {
			return nil, fmt.Errorf("parse JSON: %w", err)
		}
		if dec.More() {
			return nil, errors.New("parse JSON: more than one value")
		}
	case ".yaml", ".yml":
		if err := yaml.Unmarshal(b, &v); err != nil {
			return nil, fmt.Errorf("parse YAML: %w", err)
		}
	default:
		return nil, errors.New("must be a .json, .yaml or .yml file")
	}
	if v == nil {
		return nil, errors.New("the file is empty")
	}
	return plainJSON(v)
}

// plainJSON round-trips v through JSON, so a schema decoded from YAML and
// the same schema decoded from JSON compare and hash the same.
func plainJSON(v any) (any, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("not JSON: %w", err)
	}
	var out any
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	return out, nil
}
