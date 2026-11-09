// Package gitinfo reads the commit a project is applied from.
package gitinfo

import (
	"context"
	"os/exec"
	"strings"
	"time"

	"github.com/solaria-sh/solaria/internal/state"
)

// Source returns the HEAD commit of the git worktree holding dir, and
// whether the worktree has uncommitted changes. Outside a git worktree, or
// without git installed, it returns an empty Source: provenance is useful,
// not required.
//
// Paths in exclude, relative to dir, don't count as changes: solaria lock
// passes the lock file, which is usually modified while it runs.
func Source(dir string, exclude ...string) state.Source {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	commit, err := git(ctx, dir, "rev-parse", "--verify", "--quiet", "HEAD")
	if err != nil {
		return state.Source{}
	}
	args := []string{"status", "--porcelain", "--untracked-files=normal"}
	if len(exclude) > 0 {
		args = append(args, "--", ":(top)")
	}
	for _, p := range exclude {
		args = append(args, ":(exclude)"+p)
	}
	status, err := git(ctx, dir, args...)
	return state.Source{Commit: commit, Dirty: err == nil && status != ""}
}

func git(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	return strings.TrimSpace(string(out)), err
}
