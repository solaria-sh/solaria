package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/solaria-sh/solaria/internal/api/v1alpha1"
	"github.com/solaria-sh/solaria/internal/config"
)

func newForceUnlockCommand() *cobra.Command {
	var f projectFlags
	cmd := &cobra.Command{
		Use:   "force-unlock -e <environment> <nonce> [path...]",
		Short: "Release an environment's lock left behind by a stopped run",
		Long: `Force-unlock removes an environment's lock. Use it only when no one else is
applying: the lock error shows who took it, when, and the nonce to pass here.
For state in Azure Blob Storage it breaks the blob's lease; a stopped run's
lease also expires on its own within a minute.`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			paths := args[1:]
			if len(paths) == 0 {
				paths = []string{"."}
			}
			// The configuration says where the state is. It only needs to
			// load as far as the environment, so a project mid-edit can
			// still be unlocked.
			set, _ := config.Load(paths)
			if set != nil && set.Lookup(v1alpha1.KindEnvironment, f.env) == nil && set.Exists(v1alpha1.KindEnvironment, f.env) {
				return fmt.Errorf("environment %s doesn't load, so where its state is kept is unknown; run solaria validate", f.env)
			}
			backend, err := stateBackend(set, f.env, f.stateDir)
			if err != nil {
				return err
			}
			if err := backend.ForceUnlock(f.env, args[0]); err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Environment %s is unlocked.\n", f.env)
			return err
		},
	}
	f.register(cmd, true)
	return cmd
}
