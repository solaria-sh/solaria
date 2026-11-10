// Package scaffold creates new Solaria projects from templates (ADR 0015).
//
// A template is an ordinary Solaria project: a directory of files that is
// copied as it is, with no variables. Templates are either embedded in the
// binary, under templates/, or fetched from a git repository. Nothing in a
// template is ever executed.
package scaffold

import (
	"bytes"
	"context"
	"embed"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

//go:embed templates
var embedded embed.FS

// Default is the template used when none is named.
const Default = "minimal"

// Names returns the names of the embedded templates, sorted.
func Names() []string {
	entries, _ := fs.ReadDir(embedded, "templates")
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	return names
}

// Source says where a template comes from: an embedded template's name, or a
// git repository.
type Source struct {
	// Name is the embedded template's name, or empty for a git template.
	Name string
	// URL is the git repository, as git accepts it.
	URL string
	// Subdir is the template's directory inside the repository, as a
	// cleaned slash path, or empty for the repository's root.
	Subdir string
	// Ref is the tag, branch or commit to copy, or empty for the default
	// branch.
	Ref string
}

// String formats the source the way Parse reads it.
func (s Source) String() string {
	if s.Name != "" {
		return s.Name
	}
	out := s.URL
	if s.Subdir != "" {
		out += "//" + s.Subdir
	}
	if s.Ref != "" {
		out += "?ref=" + url.QueryEscape(s.Ref)
	}
	return out
}

var nameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// Parse reads a --template value. A bare name such as "minimal" names an
// embedded template. Anything else is a git source in the shape Terraform
// uses for module sources: <git URL>[//<subdir>][?ref=<ref>], with an
// optional git:: prefix.
func Parse(v string) (Source, error) {
	if v == "" {
		v = Default
	}
	if nameRe.MatchString(v) {
		if slices.Contains(Names(), v) {
			return Source{Name: v}, nil
		}
		return Source{}, fmt.Errorf("unknown template %q: the embedded templates are %s, and any other template is a git source such as https://github.com/<owner>/<repo>//<dir>?ref=<tag>",
			v, strings.Join(Names(), ", "))
	}

	s := strings.TrimPrefix(v, "git::")
	var src Source
	if i := strings.LastIndex(s, "?"); i >= 0 {
		q, err := url.ParseQuery(s[i+1:])
		if err != nil {
			return Source{}, fmt.Errorf("template %q: %w", v, err)
		}
		for k := range q {
			if k != "ref" {
				return Source{}, fmt.Errorf("template %q: unknown parameter %q; only ref is supported", v, k)
			}
		}
		src.Ref = q.Get("ref")
		if src.Ref == "" {
			return Source{}, fmt.Errorf("template %q: ref is empty", v)
		}
		s = s[:i]
	}
	// The subdirectory follows the first "//" after the scheme's "://".
	start := 0
	if i := strings.Index(s, "://"); i >= 0 {
		start = i + len("://")
	}
	if i := strings.Index(s[start:], "//"); i >= 0 {
		sub := s[start+i+2:]
		s = s[:start+i]
		clean := path.Clean(sub)
		if sub == "" || path.IsAbs(sub) || clean == ".." || strings.HasPrefix(clean, "../") {
			return Source{}, fmt.Errorf("template %q: %q isn't a directory inside the repository", v, sub)
		}
		if clean != "." {
			src.Subdir = clean
		}
	}
	if s == "" {
		return Source{}, fmt.Errorf("template %q: no repository URL", v)
	}
	src.URL = s
	return src, nil
}

// Fetch puts the template's files under dir, which must be an empty
// directory, and returns the directory that holds them. For a git template it
// also returns the commit it copied.
func Fetch(ctx context.Context, src Source, dir string) (root, commit string, err error) {
	if src.Name != "" {
		sub, err := fs.Sub(embedded, "templates/"+src.Name)
		if err != nil {
			return "", "", err
		}
		if err := os.CopyFS(dir, sub); err != nil {
			return "", "", fmt.Errorf("template %s: %w", src.Name, err)
		}
		return dir, "", nil
	}

	if _, err := exec.LookPath("git"); err != nil {
		return "", "", errors.New("git templates need the git command, which isn't on PATH")
	}
	ref := src.Ref
	if ref == "" {
		ref = "HEAD"
	}
	// A fetch by name works for branches, tags and commits alike, which a
	// shallow clone doesn't.
	for _, args := range [][]string{
		{"init", "--quiet"},
		{"fetch", "--quiet", "--depth", "1", "--no-tags", "--", src.URL, ref},
		{"checkout", "--quiet", "--detach", "FETCH_HEAD"},
	} {
		if _, err := git(ctx, dir, args...); err != nil {
			return "", "", fmt.Errorf("fetch template %s: %w", src, err)
		}
	}
	commit, err = git(ctx, dir, "rev-parse", "HEAD")
	if err != nil {
		return "", "", fmt.Errorf("fetch template %s: %w", src, err)
	}

	root = dir
	if src.Subdir != "" {
		root = filepath.Join(dir, filepath.FromSlash(src.Subdir))
		if fi, err := os.Stat(root); err != nil || !fi.IsDir() {
			return "", "", fmt.Errorf("template %s: the repository has no directory %s at %s", src, src.Subdir, short(commit))
		}
	}
	return root, commit, nil
}

func git(ctx context.Context, dir string, args ...string) (string, error) {
	// The ext:: transport runs a command named in the URL; never allow it,
	// whatever the user's git config says.
	cmd := exec.CommandContext(ctx, "git", append([]string{"-c", "protocol.ext.allow=never"}, args...)...)
	cmd.Dir = dir
	// Fail rather than ask for a user name or password: init never prompts.
	// Credential helpers still apply.
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return "", fmt.Errorf("git %s: %s", args[0], msg)
		}
		return "", fmt.Errorf("git %s: %w", args[0], err)
	}
	return strings.TrimSpace(string(out)), nil
}

func short(commit string) string {
	if len(commit) > 12 {
		return commit[:12]
	}
	return commit
}

// Files lists the template's files under root as slash paths, in lexical
// order. It skips .git and .solaria directories. Anything that isn't a
// regular file or a directory, such as a symbolic link, is an error: init
// copies only file contents it can see.
func Files(root string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		switch {
		case d.IsDir():
			if p != root && (d.Name() == ".git" || d.Name() == ".solaria") {
				return fs.SkipDir
			}
		case d.Type().IsRegular():
			files = append(files, rel)
		default:
			return fmt.Errorf("%s isn't a regular file; templates may only hold files and directories", rel)
		}
		return nil
	})
	return files, err
}

// Gitignore is the file name init merges rather than copies.
const Gitignore = ".gitignore"

// Conflicts returns the files that copying the template into target would
// overwrite. The top-level .gitignore never conflicts: it's merged.
func Conflicts(files []string, target string) []string {
	var out []string
	for _, f := range files {
		if f == Gitignore {
			continue
		}
		if blocked(target, f) {
			out = append(out, f)
		}
	}
	return out
}

// blocked reports whether the file at slash path f under target can't be
// created: it exists, or one of its parents exists and isn't a directory.
// The parents are checked one by one because Windows reports a file under a
// file as missing, where Unix reports "not a directory".
func blocked(target, f string) bool {
	p := target
	parts := strings.Split(f, "/")
	for i, part := range parts {
		p = filepath.Join(p, part)
		fi, err := os.Lstat(p)
		if errors.Is(err, fs.ErrNotExist) {
			return false
		}
		if err != nil || i == len(parts)-1 || !fi.IsDir() {
			return true
		}
	}
	return false
}

// Result is what Write did.
type Result struct {
	// Written lists the files created, in order, including .gitignore when
	// it didn't exist.
	Written []string
	// GitignoreUpdated is set when an existing .gitignore gained lines.
	GitignoreUpdated bool
}

// Write copies files from root into target, creating target if needed. Check
// Conflicts first: Write fails rather than overwrite a file. It then makes
// sure target's .gitignore ignores .solaria/, adding the lines of the
// template's own .gitignore that it doesn't have yet.
func Write(root string, files []string, target string) (Result, error) {
	var res Result
	if err := os.MkdirAll(target, 0o755); err != nil {
		return res, err
	}
	var templateIgnore []byte
	for _, f := range files {
		src := filepath.Join(root, filepath.FromSlash(f))
		if f == Gitignore {
			data, err := os.ReadFile(src)
			if err != nil {
				return res, err
			}
			templateIgnore = data
			continue
		}
		if err := copyFile(src, filepath.Join(target, filepath.FromSlash(f))); err != nil {
			return res, err
		}
		res.Written = append(res.Written, f)
	}

	created, updated, err := mergeGitignore(filepath.Join(target, Gitignore), templateIgnore)
	if err != nil {
		return res, err
	}
	if created {
		res.Written = append(res.Written, Gitignore)
	}
	res.GitignoreUpdated = updated
	return res, nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	fi, err := in.Stat()
	if err != nil {
		return err
	}
	mode := os.FileMode(0o644)
	if fi.Mode().Perm()&0o111 != 0 {
		mode = 0o755
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

// defaultIgnore is the .gitignore init writes when neither the project nor
// the template has one.
const defaultIgnore = "# Solaria state, locks and rendered files\n.solaria/\n"

// mergeGitignore makes the .gitignore at p ignore .solaria/ and hold every
// line of the template's .gitignore. It creates the file when it's missing.
func mergeGitignore(p string, template []byte) (created, updated bool, err error) {
	existing, err := os.ReadFile(p)
	if errors.Is(err, fs.ErrNotExist) {
		content := template
		if len(content) == 0 {
			content = []byte(defaultIgnore)
		} else if !ignoresSolaria(lines(content)) {
			content = append(withNewline(content), []byte(".solaria/\n")...)
		}
		return true, false, os.WriteFile(p, content, 0o644)
	}
	if err != nil {
		return false, false, err
	}

	have := lines(existing)
	var add []string
	for _, l := range lines(template) {
		if l != "" && !strings.HasPrefix(l, "#") && !slices.Contains(have, l) && !slices.Contains(add, l) {
			add = append(add, l)
		}
	}
	if !ignoresSolaria(have) && !ignoresSolaria(add) {
		add = append(add, ".solaria/")
	}
	if len(add) == 0 {
		return false, false, nil
	}
	var b bytes.Buffer
	b.Write(withNewline(existing))
	if len(existing) > 0 {
		b.WriteString("\n")
	}
	b.WriteString("# Added by solaria init\n")
	b.WriteString(strings.Join(add, "\n") + "\n")
	return false, true, os.WriteFile(p, b.Bytes(), 0o644)
}

func lines(data []byte) []string {
	var out []string
	for _, l := range strings.Split(string(data), "\n") {
		out = append(out, strings.TrimSpace(l))
	}
	return out
}

func ignoresSolaria(lines []string) bool {
	for _, l := range lines {
		switch strings.TrimSuffix(strings.TrimPrefix(l, "/"), "*") {
		case ".solaria", ".solaria/":
			return true
		}
	}
	return false
}

func withNewline(data []byte) []byte {
	if len(data) > 0 && data[len(data)-1] != '\n' {
		return append(slices.Clone(data), '\n')
	}
	return data
}
