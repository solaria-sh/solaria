// Command site builds solaria.sh: the landing page, and the documentation
// rendered from the repository's docs/ folder with the design system in
// DESIGN.md. The Markdown stays the source of truth; this only renders it.
//
//	go run .                 # build into public/
//	go run . -serve :4173    # build, then serve and rebuild on each page load
package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer/html"
	"github.com/yuin/goldmark/text"
)

// RepoURL is the one place the repository's address is written down.
const RepoURL = "https://github.com/solaria-sh/solaria"

// SiteURL is where the site is published. Link previews (og:url, og:image)
// need absolute URLs.
const SiteURL = "https://solaria.sh"

// SocialImage is the card X, Slack and others show for a link to the site.
// brand/build.py renders it from the brand kit; it's 1280x640.
const SocialImage = "/social-preview.png"

func main() {
	out := flag.String("out", "public", "folder to write the site into")
	repo := flag.String("repo", "..", "the Solaria repository, for docs/")
	serve := flag.String("serve", "", "after building, serve the site on this address, for example :4173")
	flag.Parse()

	b := &builder{repo: *repo, site: ".", out: *out}
	if err := b.build(); err != nil {
		log.Fatal(err)
	}
	if *serve == "" {
		return
	}
	var mu sync.Mutex
	files := http.FileServer(http.Dir(*out))
	log.Printf("serving %s on http://localhost%s", *out, *serve)
	log.Fatal(http.ListenAndServe(*serve, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/") || strings.HasSuffix(r.URL.Path, ".html") {
			mu.Lock()
			err := b.build()
			mu.Unlock()
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		}
		files.ServeHTTP(w, r)
	})))
}

// A page is one Markdown file published as one URL.
type page struct {
	src   string // repository path, such as docs/concepts.md
	url   string // site path, such as /docs/concepts/
	label string // name in the sidebar
	group string // sidebar group
}

type navItem struct {
	Label, URL string
	Active     bool
}

type navGroup struct {
	Title string
	Items []navItem
}

type heading struct{ ID, Text string }

type view struct {
	Title, Heading, Description, Repo, EditURL string
	URL, Image                                 string // absolute, for link previews
	Doc                                        bool
	Body                                       template.HTML
	TOC                                        []heading
	Nav                                        []navGroup
}

type builder struct {
	repo, site, out string
	pages           []page
	byURL           map[string]page
	bySrc           map[string]page
}

// pages lists what the docs section publishes, in sidebar order.
func (b *builder) collect() error {
	b.pages = []page{
		{"docs/README.md", "/docs/", "Overview", "Docs"},
		{"docs/concepts.md", "/docs/concepts/", "Concepts", "Docs"},
		{"docs/getting-started.md", "/docs/getting-started/", "Getting started", "Docs"},
		{"docs/editors.md", "/docs/editors/", "Editors and coding agents", "Docs"},
		{"docs/resources/agent.md", "/docs/resources/agent/", "Agent", "Resources"},
		{"docs/resources/environment.md", "/docs/resources/environment/", "Environment", "Resources"},
		{"docs/resources/mcp-server.md", "/docs/resources/mcp-server/", "McpServer", "Resources"},
		{"docs/resources/vault.md", "/docs/resources/vault/", "Vault", "Resources"},
		{"docs/providers/stub.md", "/docs/providers/stub/", "stub", "Providers"},
		{"docs/providers/gitagent.md", "/docs/providers/gitagent/", "gitagent", "Providers"},
		{"docs/providers/claude-agent-sdk.md", "/docs/providers/claude-agent-sdk/", "claude-agent-sdk", "Providers"},
		{"docs/providers/managed-agents.md", "/docs/providers/managed-agents/", "managed-agents", "Providers"},
	}
	// The ADRs and spikes stay on GitHub: links to them resolve there.
	b.byURL = map[string]page{}
	b.bySrc = map[string]page{}
	for _, p := range b.pages {
		if _, err := os.Stat(filepath.Join(b.repo, p.src)); err != nil {
			return fmt.Errorf("docs page: %w", err)
		}
		b.byURL[p.url] = p
		b.bySrc[p.src] = p
	}
	return nil
}

func (b *builder) build() error {
	if err := b.collect(); err != nil {
		return err
	}
	if err := os.RemoveAll(b.out); err != nil {
		return err
	}
	if err := copyDir(filepath.Join(b.site, "static"), b.out, nil); err != nil {
		return fmt.Errorf("copy static: %w", err)
	}
	// The JSON Schemas are served at the URLs their $id names, such as
	// https://solaria.sh/schemas/v1alpha1/agent.json, so editors can fetch
	// them (ADR 0015).
	isJSON := func(name string) bool { return strings.HasSuffix(name, ".json") }
	if err := copyDir(filepath.Join(b.repo, "schemas"), filepath.Join(b.out, "schemas"), isJSON); err != nil {
		return fmt.Errorf("copy schemas: %w", err)
	}
	tmpl := func(name string) (*template.Template, error) {
		return template.ParseFiles(filepath.Join(b.site, "templates/base.html"), filepath.Join(b.site, "templates", name))
	}

	home, err := tmpl("home.html")
	if err != nil {
		return err
	}
	if err := render(home, filepath.Join(b.out, "index.html"), view{
		Title:       "Solaria: plan and apply agent definitions across hosts",
		Description: "Solaria is a control plane for AI agent definitions. Declare an agent once, then plan and apply it to any host a provider supports.",
		Repo:        RepoURL,
		URL:         SiteURL + "/",
		Image:       SiteURL + SocialImage,
	}); err != nil {
		return err
	}

	// Cloudflare Pages serves 404.html for any path it has no file for.
	// Without one it treats the site as a single-page app and answers every
	// unknown path with the home page.
	notFound, err := tmpl("404.html")
	if err != nil {
		return err
	}
	if err := render(notFound, filepath.Join(b.out, "404.html"), view{
		Title:       "Page not found · Solaria",
		Description: "There's no page at this address on solaria.sh.",
		Repo:        RepoURL,
	}); err != nil {
		return err
	}

	doc, err := tmpl("doc.html")
	if err != nil {
		return err
	}
	for _, p := range b.pages {
		src, err := os.ReadFile(filepath.Join(b.repo, p.src))
		if err != nil {
			return err
		}
		title, body, toc, err := b.markdown(p, src)
		if err != nil {
			return fmt.Errorf("render %s: %w", p.src, err)
		}
		v := view{
			Title:       title + " · Solaria docs",
			Heading:     title,
			Description: firstParagraph(src),
			Repo:        RepoURL,
			EditURL:     RepoURL + "/blob/main/" + p.src,
			URL:         SiteURL + p.url,
			Image:       SiteURL + SocialImage,
			Doc:         true,
			Body:        template.HTML(body), //nolint:gosec // rendered from the repository's own docs
			TOC:         toc,
			Nav:         b.nav(p),
		}
		dst := filepath.Join(b.out, filepath.FromSlash(strings.TrimPrefix(p.url, "/")), "index.html")
		if err := render(doc, dst, v); err != nil {
			return err
		}
	}
	return nil
}

func (b *builder) nav(cur page) []navGroup {
	var groups []navGroup
	for _, p := range b.pages {
		if len(groups) == 0 || groups[len(groups)-1].Title != p.group {
			groups = append(groups, navGroup{Title: p.group})
		}
		g := &groups[len(groups)-1]
		g.Items = append(g.Items, navItem{Label: p.label, URL: p.url, Active: p.url == cur.url})
	}
	return groups
}

var md = goldmark.New(
	goldmark.WithExtensions(extension.GFM),
	goldmark.WithParserOptions(parser.WithAutoHeadingID()),
	// The docs are this repository's own files, so their HTML is trusted.
	goldmark.WithRendererOptions(html.WithUnsafe()),
)

// markdown renders one doc. It takes the first H1 as the page title, lists
// the H2s for the page outline, and points every relative link at the site
// page it names, or at the file on GitHub when the site doesn't publish it.
func (b *builder) markdown(p page, src []byte) (string, string, []heading, error) {
	doc := md.Parser().Parse(text.NewReader(src))
	var title string
	var toc []heading
	var remove []ast.Node
	err := ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch n := n.(type) {
		case *ast.Heading:
			t := plain(n, src)
			if n.Level == 1 && title == "" {
				title = t
				remove = append(remove, n)
			} else if n.Level == 2 {
				id, _ := n.AttributeString("id")
				if s, ok := id.([]byte); ok {
					toc = append(toc, heading{ID: string(s), Text: t})
				}
			}
		case *ast.Link:
			n.Destination = []byte(b.link(p.src, string(n.Destination)))
		}
		return ast.WalkContinue, nil
	})
	if err != nil {
		return "", "", nil, err
	}
	for _, n := range remove {
		n.Parent().RemoveChild(n.Parent(), n)
	}
	if title == "" {
		return "", "", nil, errors.New("no title: the file needs a # heading")
	}
	var buf bytes.Buffer
	if err := md.Renderer().Render(&buf, src, doc); err != nil {
		return "", "", nil, err
	}
	body := strings.ReplaceAll(buf.String(), "<table>", `<div class="table-wrap"><table>`)
	body = strings.ReplaceAll(body, "</table>", "</table></div>")
	return title, body, toc, nil
}

var scheme = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9+.-]*:`)

func (b *builder) link(from, dest string) string {
	if dest == "" || strings.HasPrefix(dest, "#") || scheme.MatchString(dest) {
		return dest
	}
	target, frag, _ := strings.Cut(dest, "#")
	if frag != "" {
		frag = "#" + frag
	}
	p := path.Clean(path.Join(path.Dir(from), target))
	if pg, ok := b.bySrc[p]; ok {
		return pg.url + frag
	}
	if pg, ok := b.bySrc[path.Join(p, "README.md")]; ok {
		return pg.url + frag
	}
	kind := "blob"
	if fi, err := os.Stat(filepath.Join(b.repo, filepath.FromSlash(p))); err == nil && fi.IsDir() {
		kind = "tree"
	}
	return RepoURL + "/" + kind + "/main/" + p + frag
}

func plain(n ast.Node, src []byte) string {
	var sb strings.Builder
	_ = ast.Walk(n, func(c ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch c := c.(type) {
		case *ast.Text:
			sb.Write(c.Segment.Value(src))
		case *ast.String:
			sb.Write(c.Value)
		}
		return ast.WalkContinue, nil
	})
	return sb.String()
}

var mdSyntax = regexp.MustCompile("[`*_]|\\[([^\\]]*)\\]\\([^)]*\\)")

// firstParagraph is the page description: the first line of prose after the
// title, without Markdown syntax.
func firstParagraph(src []byte) string {
	for _, l := range strings.Split(string(src), "\n") {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "#") || strings.HasPrefix(l, "<!--") || strings.HasPrefix(l, "|") || strings.HasPrefix(l, "-") {
			continue
		}
		l = mdSyntax.ReplaceAllString(l, "$1")
		if len(l) > 200 {
			l = l[:strings.LastIndex(l[:200], " ")] + "…"
		}
		return l
	}
	return "Solaria documentation"
}

func render(t *template.Template, dst string, v view) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	var buf bytes.Buffer
	if err := t.ExecuteTemplate(&buf, "base", v); err != nil {
		return fmt.Errorf("render %s: %w", dst, err)
	}
	return os.WriteFile(dst, buf.Bytes(), 0o644)
}

// copyDir copies the files under src to dst. With a keep function, it copies
// only the files keep accepts.
func copyDir(src, dst string, keep func(name string) bool) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if keep != nil && !keep(d.Name()) {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
}
