// Package lockfile reads, writes and checks solaria.lock, the committed file
// that pins the digests each environment may run (ADR 0013).
package lockfile

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/solaria-sh/solaria/internal/api/v1alpha1"
	"github.com/solaria-sh/solaria/internal/config"
	"github.com/solaria-sh/solaria/internal/diag"
	"github.com/solaria-sh/solaria/internal/resolve"
)

// Format identifies this version of the lock file format.
const Format = "solaria.sh/lock/v1"

// DefaultPath is where the lock file lives, relative to the working
// directory.
const DefaultPath = "solaria.lock"

// File is the lock file. Its JSON form is fixed: Write always produces the
// same bytes for the same content.
type File struct {
	Format string `json:"format"`
	// DefinitionFormat is resolve.Format when the file was written. Digests
	// from another format can't be compared.
	DefinitionFormat string                  `json:"definitionFormat"`
	Environments     map[string]*Environment `json:"environments"`
}

// Environment holds the pinned agents of one environment.
type Environment struct {
	// Agents is keyed by logical ID (Agent/<name>).
	Agents map[string]Entry `json:"agents"`
}

// Entry pins one agent.
type Entry struct {
	SpecDigest     string `json:"specDigest"`
	ResolvedDigest string `json:"resolvedDigest"`
	// Commit is the source commit the digests were computed from. It is
	// empty when the worktree had uncommitted changes.
	Commit string `json:"commit,omitempty"`
}

// New returns an empty lock file in the current formats.
func New() *File {
	return &File{Format: Format, DefinitionFormat: resolve.Format, Environments: map[string]*Environment{}}
}

var (
	digestRE = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	commitRE = regexp.MustCompile(`^[0-9a-f]{40}([0-9a-f]{24})?$`)
)

// Read reads the lock file at path. A missing file is not an error: it
// returns nil, and no environment is locked.
func Read(path string) (*File, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read lock file: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var f File
	if err := dec.Decode(&f); err != nil {
		return nil, fmt.Errorf("%s: not a valid lock file: %w", path, err)
	}
	if err := f.check(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if f.Environments == nil {
		f.Environments = map[string]*Environment{}
	}
	return &f, nil
}

func (f *File) check() error {
	if f.Format != Format {
		return fmt.Errorf("format is %q; this Solaria reads %q", f.Format, Format)
	}
	if f.DefinitionFormat == "" {
		return errors.New("definitionFormat is missing")
	}
	for env, e := range f.Environments {
		if e == nil {
			return fmt.Errorf("environments.%s is null", env)
		}
		for id, a := range e.Agents {
			name, ok := strings.CutPrefix(id, v1alpha1.KindAgent+"/")
			if !ok || name == "" {
				return fmt.Errorf("environments.%s.agents: %q is not a logical ID like Agent/<name>", env, id)
			}
			if !digestRE.MatchString(a.SpecDigest) || !digestRE.MatchString(a.ResolvedDigest) {
				return fmt.Errorf("environments.%s.agents.%s: specDigest and resolvedDigest must be sha256:<64 hex digits>", env, id)
			}
			if a.Commit != "" && !commitRE.MatchString(a.Commit) {
				return fmt.Errorf("environments.%s.agents.%s: commit %q is not a full commit hash", env, id, a.Commit)
			}
		}
	}
	return nil
}

// Write writes the file to path, replacing it atomically.
func Write(path string, f *File) error {
	if f.Environments == nil {
		f.Environments = map[string]*Environment{}
	}
	for _, e := range f.Environments {
		if e.Agents == nil {
			e.Agents = map[string]Entry{}
		}
	}
	// encoding/json writes map keys sorted, and struct fields in order.
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("write lock file: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".solaria.lock-*")
	if err != nil {
		return fmt.Errorf("write lock file: %w", err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write lock file: %w", err)
	}
	if err := tmp.Chmod(0o644); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write lock file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("write lock file: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("write lock file: %w", err)
	}
	return nil
}

// Locked reports whether env is pinned.
func (f *File) Locked(env string) bool {
	return f != nil && f.Environments[env] != nil
}

// Validate checks the file against the configuration: every environment it
// pins must exist.
func (f *File) Validate(path string, set *config.Set) diag.List {
	if f == nil {
		return nil
	}
	var diags diag.List
	for _, env := range sortedKeys(f.Environments) {
		if set.Lookup(v1alpha1.KindEnvironment, env) == nil {
			diags = append(diags, diag.Errorf(path, diag.Pos{},
				"locks Environment %q, which isn't in the configuration; run %s to unpin it",
				env, Command(path, env, "--remove")))
		}
	}
	return diags
}

// Check compares the agents an environment resolves to with its pins. It
// returns nothing for an environment that isn't locked.
func (f *File) Check(path, env string, agents []resolve.Agent) diag.List {
	if !f.Locked(env) {
		return nil
	}
	if f.DefinitionFormat != resolve.Format {
		return diag.List{diag.Errorf(path, diag.Pos{},
			"written for definition format %s, but this Solaria computes digests in %s, so every digest has changed; run %s for each environment in it",
			f.DefinitionFormat, resolve.Format, Command(path, "<environment>"))}
	}
	pins := f.Environments[env].Agents
	var diags diag.List
	selected := map[string]bool{}
	for i := range agents {
		a := &agents[i]
		selected[a.ID()] = true
		pin, ok := pins[a.ID()]
		switch {
		case !ok:
			diags = append(diags, diag.Errorf(path, diag.Pos{},
				"Environment %q selects %s, which the lock doesn't list yet (resolvedDigest %s); run %s to promote it",
				env, a.ID(), a.ResolvedDigest, Command(path, env, "-a", a.Name)))
		case pin.ResolvedDigest != a.ResolvedDigest:
			diags = append(diags, diag.Errorf(path, diag.Pos{},
				"Environment %q is locked to %s at resolvedDigest %s, but the configuration resolves to %s; run %s to promote it",
				env, a.ID(), pin.ResolvedDigest, a.ResolvedDigest, Command(path, env, "-a", a.Name)))
		}
	}
	for _, id := range sortedKeys(pins) {
		if selected[id] {
			continue
		}
		name := strings.TrimPrefix(id, v1alpha1.KindAgent+"/")
		diags = append(diags, diag.Errorf(path, diag.Pos{},
			"Environment %q is locked with %s, which it no longer selects; run %s to promote its removal",
			env, id, Command(path, env, "-a", name)))
	}
	return diags
}

// Command returns the solaria lock command for env with extra arguments,
// naming the lock file when it isn't the default.
func Command(path, env string, args ...string) string {
	parts := []string{"solaria", "lock", "-e", env}
	parts = append(parts, args...)
	if filepath.Clean(path) != DefaultPath {
		parts = append(parts, "--lock-file", path)
	}
	return strings.Join(parts, " ")
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// Change is one pin that Lock added, moved or removed. Old is nil for an
// added pin, New for a removed one.
type Change struct {
	ID       string
	Old, New *Entry
}

// Lock pins env's agents to their current digests. With no names, it pins
// every agent the environment selects and drops the others. With names, it
// updates only those: a selected agent is pinned, and an agent the
// environment no longer selects is unpinned. A pin whose digests haven't
// changed is kept as it is, commit included, so moving HEAD alone changes
// nothing. It returns the changes, sorted by logical ID.
func (f *File) Lock(env string, agents []resolve.Agent, names []string, commit string) ([]Change, error) {
	byName := map[string]*resolve.Agent{}
	for i := range agents {
		byName[agents[i].Name] = &agents[i]
	}
	e := f.Environments[env]
	if e == nil {
		e = &Environment{Agents: map[string]Entry{}}
	}
	if e.Agents == nil {
		e.Agents = map[string]Entry{}
	}

	targets := names
	if len(targets) == 0 {
		for i := range agents {
			targets = append(targets, agents[i].Name)
		}
		for id := range e.Agents {
			if name := strings.TrimPrefix(id, v1alpha1.KindAgent+"/"); byName[name] == nil {
				targets = append(targets, name)
			}
		}
	}
	for _, n := range targets {
		if byName[n] == nil {
			if _, ok := e.Agents[v1alpha1.KindAgent+"/"+n]; !ok {
				return nil, fmt.Errorf("Environment %q doesn't select an Agent named %q, and the lock doesn't list one", env, n)
			}
		}
	}

	var changes []Change
	for _, n := range targets {
		id := v1alpha1.KindAgent + "/" + n
		old, had := e.Agents[id]
		a := byName[n]
		switch {
		case a == nil:
			delete(e.Agents, id)
			changes = append(changes, Change{ID: id, Old: &old})
		case had && old.SpecDigest == a.SpecDigest && old.ResolvedDigest == a.ResolvedDigest:
		default:
			pin := Entry{SpecDigest: a.SpecDigest, ResolvedDigest: a.ResolvedDigest, Commit: commit}
			e.Agents[id] = pin
			c := Change{ID: id, New: &pin}
			if had {
				c.Old = &old
			}
			changes = append(changes, c)
		}
	}
	f.Environments[env] = e
	f.DefinitionFormat = resolve.Format
	slices.SortFunc(changes, func(a, b Change) int { return strings.Compare(a.ID, b.ID) })
	return changes, nil
}

// Stale returns the environments, other than env, whose pins were written
// in another definition format and so need locking again.
func (f *File) Stale(oldFormat, env string) []string {
	if oldFormat == resolve.Format {
		return nil
	}
	var out []string
	for _, name := range sortedKeys(f.Environments) {
		if name != env {
			out = append(out, name)
		}
	}
	return out
}
