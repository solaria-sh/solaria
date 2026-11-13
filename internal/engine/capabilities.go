package engine

import (
	"fmt"
	"net/url"
	"slices"
	"strings"

	"github.com/solaria-sh/solaria/internal/api/v1alpha1"
	"github.com/solaria-sh/solaria/internal/diag"
	"github.com/solaria-sh/solaria/internal/provider"
	"github.com/solaria-sh/solaria/internal/resolve"
)

// checkCapabilities reports what an agent needs that the host can't do,
// before any host call. Errors stop the plan; warnings explain degraded
// behavior. file is the agent's file, and envFile the file of the
// Environment the target gives it, which sandbox problems are reported on.
func checkCapabilities(a *resolve.Agent, caps provider.Capabilities, file, envFile string) diag.List {
	var diags diag.List
	where := fmt.Sprintf("%s on %s/%s", a.ID(), a.Provider, a.Placement)
	errorf := func(format string, args ...any) {
		diags = append(diags, diag.Errorf(file, diag.Pos{}, "%s: %s", where, fmt.Sprintf(format, args...)))
	}
	warnf := func(format string, args ...any) {
		diags = append(diags, diag.Warnf(file, diag.Pos{}, "%s: %s", where, fmt.Sprintf(format, args...)))
	}
	envErrorf := func(format string, args ...any) {
		diags = append(diags, diag.Errorf(envFile, diag.Pos{}, "Environment %q for %s: %s", a.Environment, where, fmt.Sprintf(format, args...)))
	}
	d := a.Definition

	if caps.ModelProviders != nil {
		if mp, _, _ := strings.Cut(d.Model.Preferred, ":"); !slices.Contains(caps.ModelProviders, mp) {
			errorf("model %s isn't available; this host runs %s models only", d.Model.Preferred, strings.Join(caps.ModelProviders, ", "))
		}
	}
	if len(d.Model.Fallback) > 0 {
		if !caps.Fallback {
			warnf("model.fallback is ignored; this host has no model fallback")
		} else if caps.ModelProviders != nil {
			for _, m := range d.Model.Fallback {
				if mp, _, _ := strings.Cut(m, ":"); !slices.Contains(caps.ModelProviders, mp) {
					warnf("fallback model %s is skipped; this host runs %s models only", m, strings.Join(caps.ModelProviders, ", "))
				}
			}
		}
	}

	for _, name := range sortedKeys(d.Tools.MCP) {
		offered := Transports(d.Tools.MCP[name])
		if !slices.ContainsFunc(offered, func(t string) bool { return slices.Contains(caps.MCPTransports, t) }) {
			errorf("McpServer %q offers %s, and this host connects over %s only", name, orNone(offered), orNone(caps.MCPTransports))
		}
	}

	if a.Sandbox != nil && a.Sandbox.Image != "" && !caps.SandboxImage {
		envErrorf("spec.image isn't supported here; describe the sandbox with packages instead (ADR 0007)")
	}
	if !caps.LocalWorkspace {
		if d.ReadOnlyWorkspace() {
			errorf("workspace.access is read-only, and this host can't keep the workspace from being written: it belongs to the host's session (ADR 0017)")
		}
		if a.WorkspacePath() != "" {
			errorf("spec.inputs.workspace.path names a folder on this machine, and this host's runs don't work on one (ADR 0017)")
		}
	}
	if len(d.Repositories) > 0 && d.ReadOnlyWorkspace() {
		errorf("repositories are cloned into the workspace, which is read-only; keep the workspace read-write and make the repositories read-only instead (ADR 0017)")
	}
	for _, r := range a.Repositories() {
		switch caps.Repositories {
		case provider.RepositoriesClone:
		case provider.RepositoriesGitHub:
			if h := urlHost(r.URL); h != "github.com" {
				errorf("repository %s is on %s, and this host only attaches GitHub repositories, which its sessions clone themselves (ADR 0017)", r.Name, h)
				continue
			}
		default:
			errorf("repository %s can't be cloned here: nothing runs before this host's runs to clone it (ADR 0017)", r.Name)
			continue
		}
		if r.Access == v1alpha1.AccessReadOnly && !caps.ReadOnlyRepositories {
			errorf("repository %s is read-only, and this host can't keep its clone from being written (ADR 0017)", r.Name)
		}
	}
	if sb := a.Sandbox; sb != nil && sb.Isolation != "" && !slices.Contains(caps.Isolation, sb.Isolation) {
		envErrorf("spec.isolation %s isn't supported here; it wraps runs on the process placement, and other placements run in a sandbox of their own (ADR 0017)", sb.Isolation)
	}
	if d.Output != nil && !caps.OutputSchema {
		errorf("spec.output.schema can't be enforced here; this host doesn't support output schemas (ADR 0019)")
	}
	if len(d.Permissions.Ask) > 0 && !caps.Approvals {
		warnf("this host can't ask for approval in unattended runs, so %s will be denied", strings.Join(d.Permissions.Ask, ", "))
	}
	return diags
}

// Transports lists the MCP transports a server offers, in a stable order.
func Transports(s resolve.MCPServer) []string {
	var out []string
	if len(s.Spec.Packages) > 0 {
		out = append(out, provider.TransportStdio)
	}
	for _, r := range s.Spec.Remotes {
		if t := string(r.Type); !slices.Contains(out, t) {
			out = append(out, t)
		}
	}
	return out
}

func orNone(xs []string) string {
	if len(xs) == 0 {
		return "nothing"
	}
	return strings.Join(xs, ", ")
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// urlHost is a URL's host name, lowercased, or "" when it doesn't parse.
func urlHost(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Hostname())
}
