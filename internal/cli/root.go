// Package cli wires Solaria's command-line interface.
package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// NewRootCommand returns the top-level solaria command with all subcommands
// attached.
func NewRootCommand() *cobra.Command {
	root := &cobra.Command{
		Use:   "solaria",
		Short: "Plan and apply agent definitions across hosts",
		Long: `Solaria is a control plane for agent definitions.

Declare an agent once, then plan and apply it to any host a provider
supports: a local container, Claude Managed Agents, and more over time.
Solaria records what it deployed where, so every agent keeps one identity
wherever it runs.`,
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	root.AddCommand(
		newValidateCommand(),
		newPlanCommand(),
		newApplyCommand(),
		newStatusCommand(),
		newDigestCommand(),
		newDestroyCommand(),
		newForceUnlockCommand(),
		newSchemaCommand(),
		newVersionCommand(),
	)

	return root
}

// Main runs the solaria command with the process's arguments and returns
// its exit code.
func Main() int {
	err := NewRootCommand().Execute()
	if err == nil {
		return 0
	}
	code, quiet := exitCode(err)
	if !quiet {
		fmt.Fprintln(os.Stderr, "Error:", err)
	}
	return code
}
