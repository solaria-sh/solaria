package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/solaria-sh/solaria/internal/state"
)

func newForceUnlockCommand() *cobra.Command {
	var f projectFlags
	cmd := &cobra.Command{
		Use:   "force-unlock -e <environment> <nonce>",
		Short: "Release an environment's lock left behind by a stopped run",
		Long: `Force-unlock removes an environment's lock. Use it only when no one else is
applying: the lock error shows who took it, when, and the nonce to pass here.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := (&state.Local{Dir: f.stateDir}).ForceUnlock(f.env, args[0]); err != nil {
				return err
			}
			_, err := fmt.Fprintf(cmd.OutOrStdout(), "Environment %s is unlocked.\n", f.env)
			return err
		},
	}
	f.register(cmd, true)
	return cmd
}
