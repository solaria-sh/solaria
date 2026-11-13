package provider

import (
	"github.com/solaria-sh/solaria/internal/api/v1alpha1"
	"github.com/solaria-sh/solaria/internal/resolve"
	"github.com/solaria-sh/solaria/internal/state"
)

// SetWorkspace records an agent's workspace in its invocation (ADR 0017):
// how the command finds it (state.WorkspaceCwd or state.WorkspaceMount,
// unless the target binds a folder, state.WorkspaceBound) and what
// the agent may do to it.
func SetWorkspace(inv *state.Invocation, a *resolve.Agent, how string) {
	inv.Workspace = how
	if p := a.WorkspacePath(); p != "" {
		inv.Workspace = state.WorkspaceBound
		inv.WorkspacePath = p
	}
	inv.WorkspaceAccess = string(v1alpha1.AccessReadWrite)
	if a.Definition.ReadOnlyWorkspace() {
		inv.WorkspaceAccess = string(v1alpha1.AccessReadOnly)
	}
}
