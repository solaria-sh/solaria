package main

import (
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

func rel(base, p string) string {
	r, err := filepath.Rel(base, p)
	if err != nil {
		return p
	}
	return r
}
