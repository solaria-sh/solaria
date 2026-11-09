package cli

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/solaria-sh/solaria/internal/gitinfo"
	"github.com/solaria-sh/solaria/internal/lockfile"
	"github.com/solaria-sh/solaria/internal/provider"
	"github.com/solaria-sh/solaria/internal/resolve"
)

func newLockCommand() *cobra.Command {
	var (
		env      string
		agents   []string
		remove   bool
		lockPath string
	)
	cmd := &cobra.Command{
		Use:   "lock -e <environment> [-a <agent>]... [path...]",
		Short: "Pin an environment's agents to their current digests",
		Long: `Lock records the digests each agent of an environment resolves to in the lock
file (solaria.lock by default), which you commit. Once an environment is in
the lock, plan and apply refuse to deploy anything else to it: promoting a
change means running lock, committing, and having the diff reviewed.

Without -a, lock pins every agent the environment selects and drops pins for
agents it no longer selects. With -a, it updates only those agents. A pin
whose digests haven't changed is left as it is.

Lock reads only the configuration: no state, no host. --remove takes the
environment out of the lock, so it's no longer checked.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			f, err := lockfile.Read(lockPath)
			if err != nil {
				return err
			}
			if f == nil {
				f = lockfile.New()
			}
			out := cmd.OutOrStdout()
			if remove {
				if len(agents) > 0 {
					return fmt.Errorf("--remove unpins the whole environment; it doesn't take -a")
				}
				if !f.Locked(env) {
					_, err := fmt.Fprintf(out, "Environment %q isn't in %s; nothing to do.\n", env, lockPath)
					return err
				}
				delete(f.Environments, env)
				if err := lockfile.Write(lockPath, f); err != nil {
					return err
				}
				_, err := fmt.Fprintf(out, "Removed Environment %q from %s. Plan and apply no longer check it.\n", env, lockPath)
				return err
			}

			set, err := loadProject(cmd, args)
			if err != nil {
				return err
			}
			resolved, err := resolve.Environment(set, env)
			if err != nil {
				return err
			}
			wasLocked := f.Locked(env)
			oldFormat := f.DefinitionFormat
			source := gitinfo.Source(".", lockPath)
			commit := source.Commit
			if source.Dirty {
				commit = ""
			}
			changes, err := f.Lock(env, resolved, agents, commit)
			if err != nil {
				return err
			}
			if wasLocked && len(changes) == 0 && oldFormat == resolve.Format {
				_, err := fmt.Fprintf(out, "%s already pins Environment %q to its current digests; nothing changed.\n", lockPath, env)
				return err
			}
			if err := lockfile.Write(lockPath, f); err != nil {
				return err
			}
			if err := writeLockChanges(out, lockPath, env, changes); err != nil {
				return err
			}
			errOut := cmd.ErrOrStderr()
			if stale := f.Stale(oldFormat, env); len(stale) > 0 {
				if _, err := fmt.Fprintf(errOut, "warning: %s was written for definition format %s; lock these environments again too: %s\n",
					lockPath, oldFormat, strings.Join(stale, ", ")); err != nil {
					return err
				}
			}
			if source.Commit != "" && source.Dirty && pinned(changes) {
				if _, err := fmt.Fprintln(errOut, "note: the worktree has uncommitted changes, so the new pins record no commit"); err != nil {
					return err
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&env, "environment", "e", "", "environment to lock")
	_ = cmd.MarkFlagRequired("environment")
	cmd.Flags().StringArrayVarP(&agents, "agent", "a", nil, "lock only this agent (repeatable)")
	cmd.Flags().BoolVar(&remove, "remove", false, "take the environment out of the lock file")
	registerLockFile(cmd, &lockPath)
	return cmd
}

// registerLockFile adds the --lock-file flag.
func registerLockFile(cmd *cobra.Command, path *string) {
	cmd.Flags().StringVar(path, "lock-file", lockfile.DefaultPath, "lock file that pins environments' digests")
}

func pinned(changes []lockfile.Change) bool {
	for _, c := range changes {
		if c.New != nil {
			return true
		}
	}
	return false
}

func writeLockChanges(w io.Writer, path, env string, changes []lockfile.Change) error {
	var b strings.Builder
	if len(changes) == 0 {
		fmt.Fprintf(&b, "Locked Environment %q in %s; its pins didn't change.\n", env, path)
		_, err := io.WriteString(w, b.String())
		return err
	}
	fmt.Fprintf(&b, "Locked Environment %q in %s:\n", env, path)
	// Writes to a strings.Builder can't fail.
	tw := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
	for _, c := range changes {
		switch {
		case c.Old == nil:
			_, _ = fmt.Fprintf(tw, "  +\t%s\t%s\n", c.ID, provider.ShortDigest(c.New.ResolvedDigest))
		case c.New == nil:
			_, _ = fmt.Fprintf(tw, "  -\t%s\t%s\n", c.ID, provider.ShortDigest(c.Old.ResolvedDigest))
		default:
			_, _ = fmt.Fprintf(tw, "  ~\t%s\t%s -> %s\n", c.ID, provider.ShortDigest(c.Old.ResolvedDigest), provider.ShortDigest(c.New.ResolvedDigest))
		}
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	_, err := io.WriteString(w, b.String())
	return err
}
