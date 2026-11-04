// Package schemas embeds the published JSON Schema files, so the CLI
// validates against exactly what editors see. The files are generated from
// internal/api/v1alpha1; run `make schema` to update them.
package schemas

import (
	"embed"
	"io/fs"
	"strings"

	"github.com/renantarouco/solaria/internal/api/v1alpha1"
)

const (
	// BaseURL is where the v1alpha1 schemas are published. The domain is
	// provisional (ADR 0004).
	BaseURL = "https://solaria.sh/schemas/" + v1alpha1.Version + "/"

	// CombinedFile is the schema that accepts any v1alpha1 resource and
	// dispatches on kind. Editors should use this one.
	CombinedFile = "solaria.json"
)

//go:embed v1alpha1/*.json
var files embed.FS

// V1alpha1 returns the v1alpha1 schema files by name, such as agent.json.
func V1alpha1() fs.FS {
	sub, err := fs.Sub(files, v1alpha1.Version)
	if err != nil {
		panic(err) // the embedded directory always exists
	}
	return sub
}

// File returns the schema file name for a kind, such as agent.json for Agent.
func File(kind string) string {
	return strings.ToLower(kind) + ".json"
}
