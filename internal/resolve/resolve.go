// Package resolve turns an agent card into what runs in one environment: it
// applies the environment's overrides, replaces references with the
// resources they name, reads instruction and skill files, and computes the
// agent's two digests (ADR 0008).
//
// Resolve expects a set that passed validate; it reports problems it still
// meets as errors, not diagnostics.
package resolve

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/solaria-sh/solaria/internal/api/v1alpha1"
	"github.com/solaria-sh/solaria/internal/config"
	"github.com/solaria-sh/solaria/internal/digest"
)

// Agent is an agent resolved for one environment.
type Agent struct {
	// Name is the agent's logical name; its logical ID is Agent/<Name>.
	Name string
	// Environment, Provider and Placement say where it runs. They are not
	// part of either digest.
	Environment string
	Provider    string
	Placement   string
	// Env is the environment's own spec: sandbox, secrets, telemetry.
	Env *v1alpha1.EnvironmentSpec
	// EnvDir is the folder of the environment's file, which relative paths
	// in Env (sandbox.image, secrets.path) start from.
	EnvDir string
	// SkillDirs maps each skill's name to its folder, for providers that
	// copy skills to the host. The files to copy are the keys of
	// Definition.Skills[name].Files; read them with ReadText so they match
	// the digest.
	SkillDirs map[string]string
	// Definition is the agent after overrides: what actually runs.
	Definition Definition
	// SpecDigest identifies the agent as written, before overrides. It is
	// the same in every environment.
	SpecDigest string
	// ResolvedDigest identifies the Definition: what runs here.
	ResolvedDigest string
}

// ID returns the agent's logical ID.
func (a *Agent) ID() string { return v1alpha1.KindAgent + "/" + a.Name }

// WorkspacePath is the absolute path of the folder the environment binds
// as the agent's workspace (spec.inputs.workspace.path), or "" when the
// workspace is the folder the run starts in.
func (a *Agent) WorkspacePath() string {
	if in := a.Env.Inputs; in != nil && in.Workspace != nil && in.Workspace.Path != "" {
		return InputPath(a.EnvDir, in.Workspace.Path)
	}
	return ""
}

// InputPath resolves an environment's input path, relative to the
// environment's folder or absolute, to a clean absolute path.
func InputPath(envDir, path string) string {
	p := filepath.FromSlash(path)
	if !filepath.IsAbs(p) {
		p = filepath.Join(envDir, p)
	}
	if abs, err := filepath.Abs(p); err == nil {
		return abs
	}
	return filepath.Clean(p)
}

// SecretRefs lists the secrets a run must be given values for, sorted: the
// values of SecretVars.
func (a *Agent) SecretRefs() []string {
	var out []string
	for _, secret := range a.SecretVars() {
		out = append(out, secret)
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// SecretVars maps each environment variable a run gets to the secret that
// holds its value. An agent with vaults gets exactly its credentials: one
// for an MCP server sets a variable named after its secret, which the
// server's configuration reads, and one for a variable sets that variable.
// An agent without vaults gets every secret its MCP servers use, each under
// its own name (ADR 0016).
func (a *Agent) SecretVars() map[string]string {
	out := map[string]string{}
	if len(a.Definition.Vaults) == 0 {
		for _, m := range a.Definition.Tools.MCP {
			for _, s := range ServerSecrets(m.Spec) {
				out[s] = s
			}
		}
		return out
	}
	for _, v := range a.Definition.Vaults {
		for _, c := range v.Credentials {
			name := c.SecretRef
			if c.Env != "" {
				name = c.Env
			}
			out[name] = c.SecretRef
		}
	}
	return out
}

// ServerSecrets lists the secrets an MCP server uses in a remote's headers
// or a package's variables, sorted.
func ServerSecrets(spec v1alpha1.McpServerSpec) []string {
	var out []string
	add := func(kvs []v1alpha1.KeyValue) {
		for _, kv := range kvs {
			if kv.SecretRef != "" {
				out = append(out, kv.SecretRef)
			}
		}
	}
	for _, rem := range spec.Remotes {
		add(rem.Headers)
	}
	for _, p := range spec.Packages {
		add(p.EnvironmentVariables)
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// Environment resolves every agent the named environment receives, sorted
// by name.
func Environment(set *config.Set, name string) ([]Agent, error) {
	envRes := set.Lookup(v1alpha1.KindEnvironment, name)
	if envRes == nil {
		return nil, fmt.Errorf("no Environment named %q", name)
	}
	env := envRes.Environment()
	var out []Agent
	for _, r := range set.Kind(v1alpha1.KindAgent) {
		if !Selected(&env.Spec, r.Agent()) {
			continue
		}
		a, err := agent(set, r, env)
		if err != nil {
			return nil, fmt.Errorf("resolve %s for Environment %q: %w", r.ID(), name, err)
		}
		out = append(out, a)
	}
	slices.SortFunc(out, func(a, b Agent) int { return strings.Compare(a.Name, b.Name) })
	return out, nil
}

// SpecDigest returns the specDigest of the named agent: the agent as
// written, before any environment's overrides.
func SpecDigest(set *config.Set, name string) (string, error) {
	r := set.Lookup(v1alpha1.KindAgent, name)
	if r == nil {
		return "", fmt.Errorf("no Agent named %q", name)
	}
	spec, _, err := definition(set, r)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", r.ID(), err)
	}
	return digest.Of(spec)
}

// Selected reports whether an environment receives an agent.
func Selected(env *v1alpha1.EnvironmentSpec, a *v1alpha1.Agent) bool {
	if env.Agents == nil {
		return true
	}
	for k, v := range env.Agents.Selector.MatchLabels {
		if a.Metadata.Labels[k] != v {
			return false
		}
	}
	return true
}

func agent(set *config.Set, r *config.Resource, env *v1alpha1.Environment) (Agent, error) {
	spec, skillDirs, err := definition(set, r)
	if err != nil {
		return Agent{}, err
	}
	specDigest, err := digest.Of(spec)
	if err != nil {
		return Agent{}, err
	}
	resolved := spec
	if o, ok := env.Spec.Overrides[r.Name]; ok {
		resolved, err = override(spec, o)
		if err != nil {
			return Agent{}, err
		}
	}
	resolvedDigest, err := digest.Of(resolved)
	if err != nil {
		return Agent{}, err
	}
	return Agent{
		Name:           r.Name,
		Environment:    env.Metadata.Name,
		Provider:       env.Spec.Provider,
		Placement:      env.Spec.Placement,
		Env:            &env.Spec,
		EnvDir:         filepath.Dir(set.Lookup(v1alpha1.KindEnvironment, env.Metadata.Name).File),
		SkillDirs:      skillDirs,
		Definition:     resolved,
		SpecDigest:     specDigest,
		ResolvedDigest: resolvedDigest,
	}, nil
}

// definition builds the normalized definition of an agent as written.
func definition(set *config.Set, r *config.Resource) (Definition, map[string]string, error) {
	s := r.Agent().Spec
	d := Definition{
		Format:       Format,
		Description:  s.Description,
		Instructions: []string{},
		Model: Model{
			Preferred: s.Model.Preferred,
			Fallback:  modelRefs(s.Model.Fallback),
			Effort:    s.Model.Effort,
		},
		Tools:       Tools{Builtin: []string{}, MCP: map[string]MCPServer{}},
		Skills:      map[string]Skill{},
		Permissions: Permissions{Ask: []string{}},
		Providers:   map[string]ProviderFields{},
	}

	for _, path := range s.Instructions {
		b, err := os.ReadFile(filepath.Join(r.Dir(), filepath.FromSlash(path)))
		if err != nil {
			return Definition{}, nil, fmt.Errorf("read instructions %s: %w", path, err)
		}
		d.Instructions = append(d.Instructions, string(normalizeText(b)))
	}

	if t := s.Tools; t != nil {
		for _, b := range t.Builtin {
			d.Tools.Builtin = append(d.Tools.Builtin, string(b))
		}
		slices.Sort(d.Tools.Builtin)
		for _, ref := range t.MCP {
			srv := set.Lookup(v1alpha1.KindMcpServer, ref.Ref)
			if srv == nil {
				return Definition{}, nil, fmt.Errorf("no McpServer named %q", ref.Ref)
			}
			m := MCPServer{Spec: srv.McpServer().Spec}
			if ref.AllowTools != nil {
				m.AllowTools = sorted(ref.AllowTools)
			}
			d.Tools.MCP[ref.Ref] = m
		}
	}

	skillDirs := map[string]string{}
	for _, path := range s.Skills {
		folder := filepath.Join(r.Dir(), filepath.FromSlash(path))
		sk, err := skill(folder)
		if err != nil {
			return Definition{}, nil, fmt.Errorf("read skill %s: %w", path, err)
		}
		// validate guarantees the skill's name matches its folder.
		d.Skills[filepath.Base(folder)] = sk
		skillDirs[filepath.Base(folder)] = folder
	}

	if p := s.Permissions; p != nil {
		d.Permissions.Ask = sorted(p.Ask)
	}
	for _, ref := range s.Vaults {
		v := set.Lookup(v1alpha1.KindVault, ref.Ref)
		if v == nil {
			return Definition{}, nil, fmt.Errorf("no Vault named %q", ref.Ref)
		}
		if d.Vaults == nil {
			d.Vaults = map[string]Vault{}
		}
		d.Vaults[ref.Ref] = vault(v.Vault().Spec)
	}
	if l := s.Limits; l != nil {
		lim, err := limits(Limits{}, l)
		if err != nil {
			return Definition{}, nil, err
		}
		d.Limits = lim
	}
	if o := s.Output; o != nil {
		schema, err := OutputSchema(r.Dir(), o.Schema)
		if err != nil {
			return Definition{}, nil, fmt.Errorf("read output schema: %w", err)
		}
		d.Output = &Output{Schema: schema}
	}
	d.Workspace = workspace(s.Workspace)
	for name, fields := range s.Providers {
		d.Providers[name] = mergePatch(nil, fields).(map[string]any)
	}
	return d, skillDirs, nil
}

// override applies an environment's override to a definition, returning a
// new definition and leaving the original untouched.
func override(d Definition, o v1alpha1.AgentOverride) (Definition, error) {
	if m := o.Model; m != nil {
		if m.Preferred != "" {
			d.Model.Preferred = m.Preferred
		}
		if m.Fallback != nil {
			d.Model.Fallback = modelRefs(m.Fallback)
		}
		if m.Effort != "" {
			d.Model.Effort = m.Effort
		}
	}
	if t := o.Tools; t != nil {
		tools, err := narrowTools(d.Tools, t)
		if err != nil {
			return Definition{}, err
		}
		d.Tools = tools
	}
	if p := o.Permissions; p != nil {
		d.Permissions.Ask = slices.Compact(sorted(append(slices.Clone(d.Permissions.Ask), p.Ask...)))
	}
	if o.Tools != nil || o.Permissions != nil {
		// An ask entry for a tool this environment removed has nothing left
		// to ask about. validate reports one the override itself names.
		d.Permissions.Ask = slices.DeleteFunc(slices.Clone(d.Permissions.Ask), func(tool string) bool {
			return !d.Tools.has(tool)
		})
	}
	if o.Limits != nil {
		lim, err := limits(d.Limits, o.Limits)
		if err != nil {
			return Definition{}, err
		}
		d.Limits = lim
	}
	if w := workspace(o.Workspace); w != nil {
		// Only narrowing is allowed: validate fails on an override that
		// makes a read-only workspace writable.
		d.Workspace = w
	}
	if len(o.Providers) > 0 {
		merged := maps.Clone(d.Providers)
		for name, fields := range o.Providers {
			var base any
			if prev, ok := merged[name]; ok {
				base = prev
			}
			merged[name] = mergePatch(base, fields).(map[string]any)
		}
		d.Providers = merged
	}
	return d, nil
}

// vault normalizes a Vault: its credentials by name, with their targets.
// Values never appear here, so they never reach a digest.
func vault(spec v1alpha1.VaultSpec) Vault {
	v := Vault{Credentials: make([]Credential, 0, len(spec.Credentials))}
	for _, c := range spec.Credentials {
		cr := Credential{Name: c.Name, SecretRef: c.SecretRef, Env: c.For.Env}
		if c.For.MCPServer != nil {
			cr.MCPServer = c.For.MCPServer.Ref
		}
		v.Credentials = append(v.Credentials, cr)
	}
	slices.SortFunc(v.Credentials, func(a, b Credential) int { return strings.Compare(a.Name, b.Name) })
	return v
}

// narrowTools applies an override's tools to an agent's, returning new
// Tools. An override can only take tools away (ADR 0004); one that would
// add a tool is an error, which validate reports first with its position.
func narrowTools(t Tools, o *v1alpha1.ToolsOverride) (Tools, error) {
	out := Tools{Builtin: t.Builtin, MCP: maps.Clone(t.MCP)}
	if o.Builtin != nil {
		out.Builtin = []string{}
		for _, b := range o.Builtin {
			if !slices.Contains(t.Builtin, string(b)) {
				return Tools{}, fmt.Errorf("tools.builtin: %q is not one of the agent's built-in tools", b)
			}
			out.Builtin = append(out.Builtin, string(b))
		}
		slices.Sort(out.Builtin)
	}
	for _, m := range o.MCP {
		srv, ok := out.MCP[m.Ref]
		if !ok {
			return Tools{}, fmt.Errorf("tools.mcp: the agent doesn't use McpServer %q", m.Ref)
		}
		for _, tool := range m.AllowTools {
			if srv.AllowTools != nil && !slices.Contains(srv.AllowTools, tool) {
				return Tools{}, fmt.Errorf("tools.mcp: %q is not in the agent's allowTools of %q", tool, m.Ref)
			}
		}
		srv.AllowTools = sorted(m.AllowTools)
		out.MCP[m.Ref] = srv
	}
	return out, nil
}

// has reports whether a permissions.ask entry (a builtin name, or
// server/tool) names a tool in t.
func (t Tools) has(tool string) bool {
	server, name, isMCP := strings.Cut(tool, "/")
	if !isMCP {
		return slices.Contains(t.Builtin, tool)
	}
	srv, ok := t.MCP[server]
	return ok && (srv.AllowTools == nil || slices.Contains(srv.AllowTools, name))
}

// limits merges set fields of l over base, normalizing the timeout.
func limits(base Limits, l *v1alpha1.LimitsSpec) (Limits, error) {
	if l.MaxTurns > 0 {
		base.MaxTurns = l.MaxTurns
	}
	if l.Timeout != "" {
		t, err := time.ParseDuration(l.Timeout)
		if err != nil {
			return Limits{}, fmt.Errorf("limits.timeout: %w", err)
		}
		base.Timeout = t.String()
	}
	return base, nil
}

// mergePatch applies patch to target as an RFC 7386 JSON Merge Patch:
// objects merge key by key, null removes a key, anything else replaces. It
// never modifies its arguments.
func mergePatch(target, patch any) any {
	p, ok := patch.(map[string]any)
	if !ok {
		return patch
	}
	t, _ := target.(map[string]any)
	out := make(map[string]any, len(t)+len(p))
	maps.Copy(out, t)
	for k, v := range p {
		if v == nil {
			delete(out, k)
			continue
		}
		out[k] = mergePatch(out[k], v)
	}
	return out
}

// skill hashes every file in an Agent Skills folder. Hidden files and
// folders (.DS_Store, .git) are left out, since they aren't part of a skill
// and differ between machines.
func skill(folder string) (Skill, error) {
	files := map[string]string{}
	err := filepath.WalkDir(folder, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p != folder && d.Name()[0] == '.' {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		info, err := os.Stat(p) // follows a symlink to a file
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(folder, p)
		if err != nil {
			return err
		}
		files[filepath.ToSlash(rel)] = digest.Bytes(normalizeText(b))
		return nil
	})
	if errors.Is(err, fs.ErrNotExist) {
		return Skill{}, fmt.Errorf("folder not found")
	}
	if err != nil {
		return Skill{}, err
	}
	return Skill{Files: files}, nil
}

// ReadText reads a file the way digests see it: with CRLF line endings
// turned into LF, unless the file is binary.
func ReadText(path string) ([]byte, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return normalizeText(b), nil
}

// normalizeText turns CRLF line endings into LF in text, so a checkout on
// Windows hashes like one on Linux. Content with a NUL byte is treated as
// binary and left alone, as git does.
func normalizeText(b []byte) []byte {
	if bytes.IndexByte(b, 0) >= 0 {
		return b
	}
	return bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n"))
}

func modelRefs(refs []v1alpha1.ModelRef) []string {
	out := make([]string, 0, len(refs))
	for _, r := range refs {
		out = append(out, string(r))
	}
	return out
}

// sorted returns a sorted copy, never nil, so empty lists encode as [].
func sorted(xs []string) []string {
	out := append(make([]string, 0, len(xs)), xs...)
	slices.Sort(out)
	return out
}
