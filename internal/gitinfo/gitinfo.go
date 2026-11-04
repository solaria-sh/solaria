// Package gitinfo reads the commit a project is applied from.
package gitinfo

import (
	"context"
	"os/exec"
	"strings"
	"time"

	"github.com/renantarouco/solaria/internal/state"
)

// Source returns the HEAD commit of the git worktree holding dir, and
// whether the worktree has uncommitted changes. Outside a git worktree, or
// without git installed, it returns an empty Source: provenance is useful,
// not required.
func Source(dir string) state.Source {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	commit, err := git(ctx, dir, "rev-parse", "--verify", "--quiet", "HEAD")
	if err != nil {
		return state.Source{}
	}
	status, err := git(ctx, dir, "status", "--porcelain", "--untracked-files=normal")
	return state.Source{Commit: commit, Dirty: err == nil && status != ""}
}

func git(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	return strings.TrimSpace(string(out)), err
}
