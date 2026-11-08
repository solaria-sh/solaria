// Command schemagen writes the v1alpha1 JSON Schema files and the card
// reference generated from them. Run it from the module root:
// go run ./internal/schemagen/cmd/schemagen.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/solaria-sh/solaria/internal/schemagen"
)

func main() {
	files, err := schemagen.Generate()
	if err != nil {
		fmt.Fprintln(os.Stderr, "schemagen:", err)
		os.Exit(1)
	}
	dir := filepath.FromSlash(schemagen.OutDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "schemagen:", err)
		os.Exit(1)
	}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, files[name], 0o644); err != nil {
			fmt.Fprintln(os.Stderr, "schemagen:", err)
			os.Exit(1)
		}
		fmt.Println("wrote", filepath.ToSlash(path))
	}
	pages, err := schemagen.Reference()
	if err != nil {
		fmt.Fprintln(os.Stderr, "schemagen:", err)
		os.Exit(1)
	}
	refDir := filepath.FromSlash(schemagen.ReferenceDir)
	if err := os.MkdirAll(refDir, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "schemagen:", err)
		os.Exit(1)
	}
	names = names[:0]
	for name := range pages {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		path := filepath.Join(refDir, name)
		if err := os.WriteFile(path, pages[name], 0o644); err != nil {
			fmt.Fprintln(os.Stderr, "schemagen:", err)
			os.Exit(1)
		}
		fmt.Println("wrote", filepath.ToSlash(path))
	}
}
