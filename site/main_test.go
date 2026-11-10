package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var (
	hrefRe = regexp.MustCompile(`(?:href|src)="([^"]*)"`)
	idRe   = regexp.MustCompile(`id="([^"]*)"`)
)

// TestSiteLinks builds the site from this repository and checks that every
// link inside it lands on a page that exists, at an anchor that exists, and
// that no relative Markdown link escaped the rewrite.
func TestSiteLinks(t *testing.T) {
	out := t.TempDir()
	b := &builder{repo: "..", site: ".", out: out}
	if err := b.build(); err != nil {
		t.Fatal(err)
	}

	ids := map[string]map[string]bool{}
	pages := map[string]string{}
	err := filepath.WalkDir(out, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".html") {
			return err
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		pages[p] = string(data)
		ids[p] = map[string]bool{}
		for _, m := range idRe.FindAllStringSubmatch(string(data), -1) {
			ids[p][m[1]] = true
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) < len(b.pages)+1 {
		t.Fatalf("built %d pages, want at least %d", len(pages), len(b.pages)+1)
	}

	for file, html := range pages {
		for _, m := range hrefRe.FindAllStringSubmatch(html, -1) {
			link := m[1]
			switch {
			case strings.HasPrefix(link, "http://"), strings.HasPrefix(link, "https://"), strings.HasPrefix(link, "mailto:"):
				if strings.Contains(link, "github.com/") && strings.Contains(link, "/solaria") && !strings.Contains(link, "github.com/solaria-sh/solaria") && !strings.Contains(link, "open-gitagent") {
					t.Errorf("%s: %s points at a repository other than %s", rel(out, file), link, RepoURL)
				}
				continue
			case strings.HasSuffix(strings.SplitN(link, "#", 2)[0], ".md"):
				t.Errorf("%s: %s is a Markdown link the build didn't rewrite", rel(out, file), link)
				continue
			}
			target, frag, _ := strings.Cut(link, "#")
			dst := file
			if target != "" {
				if !strings.HasPrefix(target, "/") {
					t.Errorf("%s: %s is relative; site links start with /", rel(out, file), link)
					continue
				}
				dst = filepath.Join(out, filepath.FromSlash(target))
				if strings.HasSuffix(target, "/") {
					dst = filepath.Join(dst, "index.html")
				}
				if _, err := os.Stat(dst); err != nil {
					t.Errorf("%s: %s doesn't exist", rel(out, file), link)
					continue
				}
			}
			if frag != "" && strings.HasSuffix(dst, ".html") && !ids[dst][frag] {
				t.Errorf("%s: %s has no #%s anchor", rel(out, file), target, frag)
			}
		}
	}
}

func TestLinkRewrite(t *testing.T) {
	b := &builder{repo: "..", site: ".", out: t.TempDir()}
	if err := b.collect(); err != nil {
		t.Fatal(err)
	}
	cases := map[[2]string]string{
		{"docs/README.md", "concepts.md"}:                        "/docs/concepts/",
		{"docs/README.md", "adr/"}:                               RepoURL + "/tree/main/docs/adr",
		{"docs/README.md", "../examples/hello"}:                  RepoURL + "/tree/main/examples/hello",
		{"docs/providers/stub.md", "../concepts.md#the-sandbox"}: "/docs/concepts/#the-sandbox",
		{"docs/concepts.md", "adr/"}:                             RepoURL + "/tree/main/docs/adr",
		{"docs/adr/README.md", "template.md"}:                    RepoURL + "/blob/main/docs/adr/template.md",
		{"docs/concepts.md", "https://agentskills.io"}:           "https://agentskills.io",
		{"docs/concepts.md", "#identity"}:                        "#identity",
	}
	for in, want := range cases {
		if got := b.link(in[0], in[1]); got != want {
			t.Errorf("link(%q, %q) = %q, want %q", in[0], in[1], got, want)
		}
	}
}

// TestHostingFiles checks the files Cloudflare Pages reads from the build:
// a top-level 404.html, without which Pages answers every unknown path with
// the home page, and the _headers rules that serve the install scripts as
// text.
func TestHostingFiles(t *testing.T) {
	out := t.TempDir()
	b := &builder{repo: "..", site: ".", out: out}
	if err := b.build(); err != nil {
		t.Fatal(err)
	}

	page, err := os.ReadFile(filepath.Join(out, "404.html"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(page), "<title>Page not found") {
		t.Error("404.html doesn't have the not-found title")
	}

	rules := headerRules(t, out)
	for _, script := range []string{"/install.sh", "/install.ps1"} {
		if _, err := os.Stat(filepath.Join(out, script)); err != nil {
			t.Errorf("%s isn't in the build: %v", script, err)
		}
		found := false
		for _, h := range rules[script] {
			if strings.EqualFold(h, "Content-Type: text/plain; charset=utf-8") {
				found = true
			}
		}
		if !found {
			t.Errorf("_headers doesn't serve %s as text/plain; it has %q", script, rules[script])
		}
	}
}

// TestSchemas checks that every JSON Schema in the repository is in the build
// at the path its $id names, byte for byte, and that _headers lets editors in
// a browser fetch it (ADR 0015).
func TestSchemas(t *testing.T) {
	out := t.TempDir()
	b := &builder{repo: "..", site: ".", out: out}
	if err := b.build(); err != nil {
		t.Fatal(err)
	}

	const site = "https://solaria.sh/"
	n := 0
	err := filepath.WalkDir(filepath.Join("..", "schemas"), func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".json") {
			return err
		}
		n++
		want, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		var schema struct {
			ID string `json:"$id"`
		}
		if err := json.Unmarshal(want, &schema); err != nil {
			return fmt.Errorf("%s: %w", p, err)
		}
		if !strings.HasPrefix(schema.ID, site) {
			t.Errorf("%s: $id %q isn't on %s", p, schema.ID, site)
			return nil
		}
		got, err := os.ReadFile(filepath.Join(out, filepath.FromSlash(strings.TrimPrefix(schema.ID, site))))
		if err != nil {
			t.Errorf("%s: not served at its $id: %v", p, err)
			return nil
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s: the copy at %s differs", p, schema.ID)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		t.Fatal("found no schemas under ../schemas")
	}

	found := false
	for _, h := range headerRules(t, out)["/schemas/*"] {
		if strings.EqualFold(h, "Access-Control-Allow-Origin: *") {
			found = true
		}
	}
	if !found {
		t.Error("_headers doesn't allow other origins to fetch /schemas/*")
	}
}

// headerRules parses the built _headers file into the headers set for each
// path pattern.
func headerRules(t *testing.T, out string) map[string][]string {
	t.Helper()
	headers, err := os.ReadFile(filepath.Join(out, "_headers"))
	if err != nil {
		t.Fatal(err)
	}
	rules := map[string][]string{}
	var path string
	for _, l := range strings.Split(string(headers), "\n") {
		switch {
		case strings.TrimSpace(l) == "", strings.HasPrefix(strings.TrimSpace(l), "#"):
		case !strings.HasPrefix(l, " ") && !strings.HasPrefix(l, "\t"):
			path = strings.TrimSpace(l)
		default:
			rules[path] = append(rules[path], strings.TrimSpace(l))
		}
	}
	return rules
}

func rel(base, p string) string {
	r, err := filepath.Rel(base, p)
	if err != nil {
		return p
	}
	return r
}
