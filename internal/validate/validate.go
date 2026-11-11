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
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/solaria-sh/solaria/internal/api/v1alpha1"
	"github.com/solaria-sh/solaria/internal/config"
	"github.com/solaria-sh/solaria/internal/diag"
	"github.com/solaria-sh/solaria/internal/provider"
	"github.com/solaria-sh/solaria/internal/resolve"
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
	for _, r := range set.Kind(v1alpha1.KindVault) {
		diags = append(diags, vault(set, r)...)
	}
	return diags
}

// vault checks that each credential is named once and unlocks something
// that exists and uses it.
func vault(set *config.Set, r *config.Resource) diag.List {
	var diags diag.List
	errorf := func(p diag.Pos, format string, args ...any) {
		diags = append(diags, diag.Errorf(r.File, p, format, args...))
	}
	names := map[string]int{}
	for i, c := range r.Vault().Spec.Credentials {
		at := func(path ...string) diag.Pos {
			return r.Pos(append([]string{"spec", "credentials", strconv.Itoa(i)}, path...)...)
		}
		if prev, dup := names[c.Name]; dup {
			errorf(at("name"), "spec.credentials[%d].name: %q is also spec.credentials[%d]", i, c.Name, prev)
		} else {
			names[c.Name] = i
		}
		m := c.For.MCPServer
		if m == nil {
			continue
		}
		srv := set.Lookup(v1alpha1.KindMcpServer, m.Ref)
		switch {
		case !set.Exists(v1alpha1.KindMcpServer, m.Ref):
			errorf(at("for", "mcpServer", "ref"), "spec.credentials[%d].for.mcpServer.ref: no McpServer named %q", i, m.Ref)
		case srv == nil:
			// The server exists but failed to load; its own errors say why.
		case !slices.Contains(resolve.ServerSecrets(srv.McpServer().Spec), c.SecretRef):
			errorf(at("secretRef"), "spec.credentials[%d].secretRef: McpServer %q doesn't use secret %q in a header or a package variable", i, m.Ref, c.SecretRef)
		}
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

	diags = append(diags, outputSchema(r)...)

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

	diags = append(diags, vaultRefs(set, r, mcp)...)

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

// vaultRefs checks an agent's vault references and that its vaults grant
// every secret its MCP servers need. An agent without vaults gets its
// servers' secrets straight from the secret source, as before Vaults
// existed; validate suggests scoping them (ADR 0016).
func vaultRefs(set *config.Set, r *config.Resource, mcp map[string]*v1alpha1.MCPToolRef) diag.List {
	var diags diag.List
	spec := r.Agent().Spec
	refs := map[string]bool{}
	granted := map[string]bool{} // server + "/" + secretRef
	for i, v := range spec.Vaults {
		p := r.Pos("spec", "vaults", strconv.Itoa(i), "ref")
		if refs[v.Ref] {
			diags = append(diags, diag.Errorf(r.File, p, "spec.vaults[%d].ref: Vault %q is referenced twice", i, v.Ref))
			continue
		}
		refs[v.Ref] = true
		vr := set.Lookup(v1alpha1.KindVault, v.Ref)
		if vr == nil {
			if !set.Exists(v1alpha1.KindVault, v.Ref) {
				diags = append(diags, diag.Errorf(r.File, p, "spec.vaults[%d].ref: no Vault named %q", i, v.Ref))
			}
			continue
		}
		for _, c := range vr.Vault().Spec.Credentials {
			if c.For.MCPServer != nil {
				granted[c.For.MCPServer.Ref+"/"+c.SecretRef] = true
			}
		}
	}

	t := spec.Tools
	if t == nil {
		return diags
	}
	for i, ref := range t.MCP {
		srv := set.Lookup(v1alpha1.KindMcpServer, ref.Ref)
		if srv == nil || mcp[ref.Ref] != &t.MCP[i] {
			continue // missing or a duplicate, reported above
		}
		secrets := resolve.ServerSecrets(srv.McpServer().Spec)
		if len(secrets) == 0 {
			continue
		}
		p := r.Pos("spec", "tools", "mcp", strconv.Itoa(i), "ref")
		if len(spec.Vaults) == 0 {
			diags = append(diags, diag.Warnf(r.File, p, "spec.tools.mcp[%d]: McpServer %q gets %s straight from the secret source; grant it through a Vault in spec.vaults to scope it to this agent", i, ref.Ref, quoteList(secrets)))
			continue
		}
		for _, sec := range secrets {
			if !granted[ref.Ref+"/"+sec] {
				diags = append(diags, diag.Errorf(r.File, p, "spec.tools.mcp[%d]: McpServer %q needs secret %q, and none of the agent's vaults grants it for that server", i, ref.Ref, sec))
			}
		}
	}
	return diags
}

// quoteList quotes names and joins them for a message.
func quoteList(names []string) string {
	q := make([]string, len(names))
	for i, n := range names {
		q[i] = strconv.Quote(n)
	}
	if len(q) == 1 {
		return "secret " + q[0]
	}
	return "secrets " + strings.Join(q[:len(q)-1], ", ") + " and " + q[len(q)-1]
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

// narrowing checks that an override only takes access away from the agent
// (ADR 0004): it may keep fewer built-in tools, narrow a server's
// allowTools, and add tools to permissions.ask, but never add a tool.
func narrowing(r *config.Resource, name string, agent v1alpha1.AgentSpec, o v1alpha1.AgentOverride) diag.List {
	var diags diag.List
	errorf := func(p diag.Pos, format string, args ...any) {
		diags = append(diags, diag.Errorf(r.File, p, format, args...))
	}
	field := "spec.overrides." + name

	// What the agent has, and what it keeps here.
	builtin := map[string]bool{}
	mcp := map[string]*v1alpha1.MCPToolRef{}
	if t := agent.Tools; t != nil {
		for _, b := range t.Builtin {
			builtin[string(b)] = true
		}
		for i := range t.MCP {
			if _, dup := mcp[t.MCP[i].Ref]; !dup {
				mcp[t.MCP[i].Ref] = &t.MCP[i]
			}
		}
	}
	keptBuiltin, keptMCP := builtin, mcp

	if t := o.Tools; t != nil {
		if t.Builtin != nil {
			keptBuiltin = map[string]bool{}
			for i, b := range t.Builtin {
				if !builtin[string(b)] {
					errorf(r.Pos("spec", "overrides", name, "tools", "builtin", strconv.Itoa(i)),
						"%s.tools.builtin[%d]: Agent %q doesn't have %q, and an override can only remove tools", field, i, name, b)
					continue
				}
				keptBuiltin[string(b)] = true
			}
		}
		keptMCP = maps.Clone(mcp)
		seen := map[string]bool{}
		for i, m := range t.MCP {
			p := r.Pos("spec", "overrides", name, "tools", "mcp", strconv.Itoa(i), "ref")
			ref, ok := mcp[m.Ref]
			switch {
			case !ok:
				errorf(p, "%s.tools.mcp[%d].ref: Agent %q doesn't use McpServer %q, and an override can only remove tools", field, i, name, m.Ref)
				continue
			case seen[m.Ref]:
				errorf(p, "%s.tools.mcp[%d].ref: McpServer %q is narrowed twice", field, i, m.Ref)
				continue
			}
			seen[m.Ref] = true
			for j, tool := range m.AllowTools {
				if len(ref.AllowTools) > 0 && !slices.Contains(ref.AllowTools, tool) {
					errorf(r.Pos("spec", "overrides", name, "tools", "mcp", strconv.Itoa(i), "allowTools", strconv.Itoa(j)),
						"%s.tools.mcp[%d].allowTools[%d]: %q is not in Agent %q's allowTools of %q, and an override can only remove tools", field, i, j, tool, name, m.Ref)
				}
			}
			keptMCP[m.Ref] = &v1alpha1.MCPToolRef{Ref: m.Ref, AllowTools: m.AllowTools}
		}
	}

	if p := o.Permissions; p != nil {
		for i, tool := range p.Ask {
			if askTarget(tool, keptBuiltin, keptMCP) != "" {
				errorf(r.Pos("spec", "overrides", name, "permissions", "ask", strconv.Itoa(i)),
					"%s.permissions.ask[%d]: %q is not one of Agent %q's tools in this environment", field, i, tool, name)
			}
		}
	}
	if w := o.Workspace; w != nil && w.Access == v1alpha1.AccessReadWrite && agent.Workspace != nil && agent.Workspace.Access == v1alpha1.AccessReadOnly {
		errorf(r.Pos("spec", "overrides", name, "workspace", "access"),
			"%s.workspace.access: Agent %q's workspace is read-only, and an override can only narrow access", field, name)
	}
	return diags
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
		if resolve.Selected(&spec, a.Agent()) {
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
		if a := set.Lookup(v1alpha1.KindAgent, name); a != nil {
			diags = append(diags, narrowing(r, name, a.Agent().Spec, spec.Overrides[name])...)
		}
		diags = append(diags, passthrough(r, spec.Overrides[name].Providers, "spec", "overrides", name, "providers")...)
	}

	if t := spec.Telemetry; t != nil {
		for _, k := range sortedKeys(t.ResourceAttributes) {
			p := r.KeyPos("spec", "telemetry", "resourceAttributes", k)
			switch {
			case provider.ReservedAttribute(k):
				errorf(p, "spec.telemetry.resourceAttributes: %s is set by Solaria or the runtime and can't be replaced", k)
			case !provider.AttributeKey.MatchString(k):
				errorf(p, "spec.telemetry.resourceAttributes: %q is not a valid key: use letters, digits, '.', '_' and '-', starting with a letter or digit", k)
			}
		}
	}

	if in := spec.Inputs; in != nil && in.Workspace != nil && in.Workspace.Path != "" {
		if err := directory(resolve.InputPath(r.Dir(), in.Workspace.Path)); err != nil {
			errorf(r.Pos("spec", "inputs", "workspace", "path"), "spec.inputs.workspace.path: %v", err)
		}
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
		if v1alpha1.IsDockerfilePath(sb.Image) {
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

func directory(path string) error {
	info, err := os.Stat(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return fmt.Errorf("folder %s not found", path)
	case err != nil:
		return err
	case !info.IsDir():
		return fmt.Errorf("%s is a file, not a folder", path)
	}
	return nil
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
