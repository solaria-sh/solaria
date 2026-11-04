// Package validate checks a loaded set of resources for problems the schema
// can't see: references between resources, files on disk, and choices that
// cost portability.
//
// Provider capabilities (which models, transports and sandboxes a host
// accepts) are checked at plan time, once an environment's provider is known.
package validate

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/renantarouco/solaria/internal/api/v1alpha1"
	"github.com/renantarouco/solaria/internal/config"
	"github.com/renantarouco/solaria/internal/diag"
)

// Validate checks every resource in the set.
func Validate(set *config.Set) diag.List {
	var diags diag.List
	for _, r := range set.Kind(v1alpha1.KindAgent) {
		diags = append(diags, agent(set, r)...)
	}
	for _, r := range set.Kind(v1alpha1.KindEnvironment) {
		diags = append(diags, environment(set, r)...)
	}
	return diags
}

func agent(set *config.Set, r *config.Resource) diag.List {
	var diags diag.List
	spec := r.Agent().Spec
	errorf := func(p diag.Pos, format string, args ...any) {
		diags = append(diags, diag.Errorf(r.File, p, format, args...))
	}

	for i, path := range spec.Instructions {
		p := r.Pos("spec", "instructions", strconv.Itoa(i))
		if err := regularFile(r.Dir(), path); err != nil {
			errorf(p, "spec.instructions[%d]: %v", i, err)
		}
	}

	skillNames := map[string]int{}
	for i, path := range spec.Skills {
		p := r.Pos("spec", "skills", strconv.Itoa(i))
		name, err := skill(r.Dir(), path)
		if err != nil {
			errorf(p, "spec.skills[%d]: %v", i, err)
			continue
		}
		if prev, ok := skillNames[name]; ok {
			errorf(p, "spec.skills[%d]: skill %q is also spec.skills[%d]", i, name, prev)
			continue
		}
		skillNames[name] = i
	}

	builtin := map[string]bool{}
	mcp := map[string]*v1alpha1.MCPToolRef{}
	if t := spec.Tools; t != nil {
		for _, b := range t.Builtin {
			builtin[string(b)] = true
		}
		for i := range t.MCP {
			ref := &t.MCP[i]
			p := r.Pos("spec", "tools", "mcp", strconv.Itoa(i), "ref")
			if !set.Exists(v1alpha1.KindMcpServer, ref.Ref) {
				errorf(p, "spec.tools.mcp[%d].ref: no McpServer named %q", i, ref.Ref)
			}
			if _, dup := mcp[ref.Ref]; dup {
				errorf(p, "spec.tools.mcp[%d].ref: McpServer %q is referenced twice", i, ref.Ref)
				continue // keep the first reference's allowTools
			}
			mcp[ref.Ref] = ref
		}
	}

	if perm := spec.Permissions; perm != nil {
		for i, tool := range perm.Ask {
			p := r.Pos("spec", "permissions", "ask", strconv.Itoa(i))
			if msg := askTarget(tool, builtin, mcp); msg != "" {
				errorf(p, "spec.permissions.ask[%d]: %s", i, msg)
			}
		}
	}

	diags = append(diags, passthrough(r, spec.Providers, "spec", "providers")...)
	return diags
}

// askTarget explains why an ask entry names no tool the agent has, or
// returns "".
func askTarget(tool string, builtin map[string]bool, mcp map[string]*v1alpha1.MCPToolRef) string {
	server, name, isMCP := strings.Cut(tool, "/")
	if !isMCP {
		if !builtin[tool] {
			return fmt.Sprintf("%q is not in spec.tools.builtin", tool)
		}
		return ""
	}
	ref, ok := mcp[server]
	switch {
	case !ok:
		return fmt.Sprintf("%q names MCP server %q, which is not in spec.tools.mcp", tool, server)
	case name == "":
		return fmt.Sprintf("%q has no tool name after the slash", tool)
	case len(ref.AllowTools) > 0 && !slices.Contains(ref.AllowTools, name):
		return fmt.Sprintf("%q is not in the allowTools of %q", name, server)
	}
	return ""
}

func environment(set *config.Set, r *config.Resource) diag.List {
	var diags diag.List
	spec := r.Environment().Spec
	errorf := func(p diag.Pos, format string, args ...any) {
		diags = append(diags, diag.Errorf(r.File, p, format, args...))
	}
	warnf := func(p diag.Pos, format string, args ...any) {
		diags = append(diags, diag.Warnf(r.File, p, format, args...))
	}

	selected := map[string]bool{}
	for _, a := range set.Kind(v1alpha1.KindAgent) {
		if spec.Agents == nil || matches(spec.Agents.Selector.MatchLabels, a.Agent().Metadata.Labels) {
			selected[a.Name] = true
		}
	}
	if spec.Agents != nil && len(selected) == 0 {
		warnf(r.Pos("spec", "agents", "selector"), "spec.agents.selector matches no agents")
	}

	for _, name := range sortedKeys(spec.Overrides) {
		p := r.KeyPos("spec", "overrides", name)
		switch {
		case !set.Exists(v1alpha1.KindAgent, name):
			errorf(p, "spec.overrides: no Agent named %q", name)
		case set.Lookup(v1alpha1.KindAgent, name) == nil:
			// The agent exists but failed to load; its own errors say why.
		case !selected[name]:
			warnf(p, "spec.overrides: Agent %q is not selected by spec.agents.selector, so this override has no effect", name)
		}
		diags = append(diags, passthrough(r, spec.Overrides[name].Providers, "spec", "overrides", name, "providers")...)
	}

	if s := spec.Secrets; s != nil && s.Source == v1alpha1.SecretsEnv && s.Path != "" {
		errorf(r.Pos("spec", "secrets", "path"), "spec.secrets.path only applies when source is dotenv")
	}

	if sb := spec.Sandbox; sb != nil {
		if n := sb.Network; n != nil && n.Mode == v1alpha1.NetworkUnrestricted {
			if len(n.Allow) > 0 {
				warnf(r.Pos("spec", "sandbox", "network", "allow"), "spec.sandbox.network.allow is ignored when mode is unrestricted")
			}
			if n.PackageManagers {
				warnf(r.Pos("spec", "sandbox", "network", "packageManagers"), "spec.sandbox.network.packageManagers is ignored when mode is unrestricted")
			}
		}
		if looksLikePath(sb.Image) {
			if err := regularFile(r.Dir(), sb.Image); err != nil {
				errorf(r.Pos("spec", "sandbox", "image"), "spec.sandbox.image: %v", err)
			}
		}
	}
	return diags
}

// passthrough warns about provider-specific fields: they are allowed, but
// make the resource less portable, and ADR 0004 says that must be visible.
func passthrough(r *config.Resource, providers map[string]map[string]any, path ...string) diag.List {
	var diags diag.List
	field := strings.Join(path, ".")
	for _, name := range sortedKeys(providers) {
		diags = append(diags, diag.Warnf(r.File, r.KeyPos(append(slices.Clip(path), name)...),
			"%s.%s: fields for one provider make %s less portable", field, name, r.ID()))
	}
	return diags
}

func matches(want, have map[string]string) bool {
	for k, v := range want {
		if have[k] != v {
			return false
		}
	}
	return true
}

// looksLikePath tells a Dockerfile path from an image reference such as
// ghcr.io/org/image:tag.
func looksLikePath(s string) bool {
	if s == "" {
		return false
	}
	base := strings.ToLower(filepath.Base(s))
	return strings.HasPrefix(s, "./") || strings.HasPrefix(s, "../") || filepath.IsAbs(s) ||
		base == "dockerfile" || strings.HasSuffix(base, ".dockerfile")
}

func regularFile(dir, rel string) error {
	info, err := os.Stat(filepath.Join(dir, filepath.FromSlash(rel)))
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return fmt.Errorf("file %q not found", rel)
	case err != nil:
		return err
	case info.IsDir():
		return fmt.Errorf("%q is a directory, not a file", rel)
	}
	return nil
}

// skillName follows the Agent Skills specification: lowercase letters,
// digits and single hyphens, at most 64 characters.
var skillName = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// skill checks an Agent Skills folder and returns the skill's name.
func skill(dir, rel string) (string, error) {
	folder := filepath.Join(dir, filepath.FromSlash(rel))
	info, err := os.Stat(folder)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return "", fmt.Errorf("skill folder %q not found", rel)
	case err != nil:
		return "", err
	case !info.IsDir():
		return "", fmt.Errorf("%q is a file; a skill is a folder holding SKILL.md", rel)
	}
	data, err := os.ReadFile(filepath.Join(folder, "SKILL.md"))
	if errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("skill folder %q has no SKILL.md", rel)
	}
	if err != nil {
		return "", err
	}
	var fm struct {
		Name        string `yaml:"name"`
		Description string `yaml:"description"`
	}
	front, ok := frontMatter(data)
	if !ok {
		return "", fmt.Errorf("%s/SKILL.md has no YAML front matter", rel)
	}
	if err := yaml.Unmarshal(front, &fm); err != nil {
		return "", fmt.Errorf("%s/SKILL.md front matter: %w", rel, err)
	}
	folderName := filepath.Base(folder)
	switch {
	case fm.Name == "":
		return "", fmt.Errorf("%s/SKILL.md front matter has no name", rel)
	case len(fm.Name) > 64 || !skillName.MatchString(fm.Name):
		return "", fmt.Errorf("%s/SKILL.md: name %q must be lowercase letters, digits and hyphens, at most 64 characters", rel, fm.Name)
	case fm.Name != folderName:
		return "", fmt.Errorf("%s/SKILL.md: name %q must match the folder name %q", rel, fm.Name, folderName)
	case strings.TrimSpace(fm.Description) == "":
		return "", fmt.Errorf("%s/SKILL.md front matter has no description", rel)
	case len(fm.Description) > 1024:
		return "", fmt.Errorf("%s/SKILL.md: description is longer than 1024 characters", rel)
	}
	return fm.Name, nil
}

func frontMatter(data []byte) ([]byte, bool) {
	data = bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n"))
	rest, ok := bytes.CutPrefix(data, []byte("---\n"))
	if !ok {
		return nil, false
	}
	if bytes.HasPrefix(rest, []byte("---\n")) {
		return nil, true
	}
	front, _, ok := bytes.Cut(rest, []byte("\n---"))
	return front, ok
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
