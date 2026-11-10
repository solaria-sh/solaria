package scaffold

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	cases := map[string]Source{
		"":                       {Name: "minimal"},
		"minimal":                {Name: "minimal"},
		"https://github.com/o/r": {URL: "https://github.com/o/r"},
		"https://github.com/o/r.git//examples/x?ref=v1.2.0":   {URL: "https://github.com/o/r.git", Subdir: "examples/x", Ref: "v1.2.0"},
		"git::https://example.com/r//a/./b/?ref=main":         {URL: "https://example.com/r", Subdir: "a/b", Ref: "main"},
		"git@github.com:o/r.git//t":                           {URL: "git@github.com:o/r.git", Subdir: "t"},
		"file:///srv/repos/t.git//one?ref=0123456789abcdef01": {URL: "file:///srv/repos/t.git", Subdir: "one", Ref: "0123456789abcdef01"},
		"ssh://git@host/r//.":                                 {URL: "ssh://git@host/r"},
	}
	for in, want := range cases {
		got, err := Parse(in)
		if err != nil {
			t.Errorf("Parse(%q): %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("Parse(%q) = %+v, want %+v", in, got, want)
		}
		if again, err := Parse(got.String()); err != nil || again != got {
			t.Errorf("Parse(%q).String() = %q doesn't parse back: %+v, %v", in, got.String(), again, err)
		}
	}
}

func TestParseRejects(t *testing.T) {
	for in, want := range map[string]string{
		"reviewer":                          `unknown template "reviewer"`,
		"https://h/r?ref=":                  "ref is empty",
		"https://h/r?branch=main":           `unknown parameter "branch"`,
		"https://h/r//../up":                "isn't a directory inside the repository",
		"https://h/r//":                     "isn't a directory inside the repository",
		"https://h/r///abs":                 "isn't a directory inside the repository",
		"//sub":                             "no repository URL",
		"https://h/r//a/../../b?ref=v1.0.0": "isn't a directory inside the repository",
	} {
		_, err := Parse(in)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("Parse(%q) = %v, want an error containing %q", in, err, want)
		}
	}
}

func TestEmbeddedTemplates(t *testing.T) {
	names := Names()
	if !slices.Contains(names, Default) {
		t.Fatalf("Names() = %v, want it to include the default %q", names, Default)
	}
	for _, name := range names {
		dir := t.TempDir()
		root, commit, err := Fetch(context.Background(), Source{Name: name}, dir)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if commit != "" {
			t.Errorf("%s: an embedded template has no commit, got %q", name, commit)
		}
		files, err := Files(root)
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range files {
			if !strings.HasSuffix(f, ".yaml") {
				continue
			}
			data, err := os.ReadFile(filepath.Join(root, f))
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasPrefix(string(data), "# yaml-language-server: $schema=https://solaria.sh/schemas/v1alpha1/solaria.json\n") {
				t.Errorf("%s/%s doesn't start with the schema modeline", name, f)
			}
		}
	}
}

func TestFiles(t *testing.T) {
	root := t.TempDir()
	write(t, root, "solaria.yaml", "a")
	write(t, root, "skills/x/SKILL.md", "b")
	write(t, root, ".git/config", "c")
	write(t, root, ".solaria/state/local.json", "d")
	write(t, root, "sub/.solaria/x", "e")
	write(t, root, ".gitignore", "f")

	got, err := Files(root)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{".gitignore", "skills/x/SKILL.md", "solaria.yaml"}
	if !slices.Equal(got, want) {
		t.Errorf("Files = %v, want %v", got, want)
	}

	if runtime.GOOS == "windows" {
		return
	}
	if err := os.Symlink("/etc/passwd", filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	if _, err := Files(root); err == nil || !strings.Contains(err.Error(), "link isn't a regular file") {
		t.Errorf("Files with a symbolic link: %v, want an error naming it", err)
	}
}

func TestConflictsAndWrite(t *testing.T) {
	root := t.TempDir()
	write(t, root, "solaria.yaml", "agent")
	write(t, root, "instructions/role.md", "role")
	write(t, root, ".gitignore", "node_modules/\n")
	files, err := Files(root)
	if err != nil {
		t.Fatal(err)
	}

	target := t.TempDir()
	write(t, target, "instructions", "a file where a directory goes")
	write(t, target, ".gitignore", "bin/")
	if got := Conflicts(files, target); !slices.Equal(got, []string{"instructions/role.md"}) {
		t.Errorf("Conflicts = %v, want [instructions/role.md]", got)
	}

	target = filepath.Join(t.TempDir(), "new", "project")
	if got := Conflicts(files, target); len(got) != 0 {
		t.Errorf("Conflicts in a missing directory = %v, want none", got)
	}
	res, err := Write(root, files, target)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"instructions/role.md", "solaria.yaml", ".gitignore"}; !slices.Equal(res.Written, want) || res.GitignoreUpdated {
		t.Errorf("Write = %+v, want Written %v", res, want)
	}
	if got := read(t, target, ".gitignore"); got != "node_modules/\n.solaria/\n" {
		t.Errorf(".gitignore = %q", got)
	}
	if got := read(t, target, "instructions/role.md"); got != "role" {
		t.Errorf("instructions/role.md = %q", got)
	}
	if _, err := Write(root, files, target); err == nil {
		t.Error("a second Write overwrote files")
	}
}

func TestMergeGitignore(t *testing.T) {
	cases := []struct {
		name, existing, template, want string
		created, updated               bool
	}{
		{name: "missing, no template", existing: "-", want: defaultIgnore, created: true},
		{name: "missing, template ignores solaria", existing: "-", template: "/.solaria\n", want: "/.solaria\n", created: true},
		{name: "already ignored", existing: "bin/\n.solaria/\n", want: "bin/\n.solaria/\n"},
		{name: "glob counts", existing: "/.solaria/*", want: "/.solaria/*"},
		{name: "append", existing: "bin/", template: "# deps\nnode_modules/\nbin/\n", want: "bin/\n\n# Added by solaria init\nnode_modules/\n.solaria/\n", updated: true},
		{name: "empty file", existing: "", want: "# Added by solaria init\n.solaria/\n", updated: true},
	}
	for _, c := range cases {
		p := filepath.Join(t.TempDir(), ".gitignore")
		if c.existing != "-" {
			if err := os.WriteFile(p, []byte(c.existing), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		created, updated, err := mergeGitignore(p, []byte(c.template))
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		got, _ := os.ReadFile(p)
		if string(got) != c.want || created != c.created || updated != c.updated {
			t.Errorf("%s: got %q created=%v updated=%v, want %q created=%v updated=%v",
				c.name, got, created, updated, c.want, c.created, c.updated)
		}
	}
}

func write(t *testing.T, root, name, content string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, root, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
