package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/solaria-sh/solaria/internal/engine"
	"github.com/solaria-sh/solaria/internal/provider"
)

func newPlanCommand() *cobra.Command {
	var (
		f        projectFlags
		refresh  bool
		detailed bool
		destroy  bool
		rotate   bool
	)
	cmd := &cobra.Command{
		Use:   "plan -e <environment> [path...]",
		Short: "Show what apply would change",
		Long: `Plan resolves every agent the environment receives, checks it against the
provider's capabilities, refreshes the state from the host, and shows what
apply would create, update, replace or delete. It changes nothing.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			set, err := loadProject(cmd, args)
			if err != nil {
				return err
			}
			e, err := newEngine(cmd, set, &f)
			if err != nil {
				return err
			}
			plan, err := makePlan(cmd, e, f.env, engine.PlanOptions{Refresh: refresh, Destroy: destroy, RotateSecrets: rotate})
			if err != nil {
				return err
			}
			if err := engine.Render(cmd.OutOrStdout(), plan); err != nil {
				return err
			}
			if detailed && plan.HasChanges() {
				return &exitError{code: 2}
			}
			return nil
		},
	}
	f.register(cmd, true)
	registerLockFile(cmd, &f.lockFile)
	cmd.Flags().BoolVar(&refresh, "refresh", true, "ask the host for the current state of every agent first")
	cmd.Flags().BoolVar(&detailed, "detailed-exitcode", false, "exit 0 for no changes, 2 for changes, 1 for errors")
	cmd.Flags().BoolVar(&destroy, "destroy", false, "plan the removal of everything in the environment")
	cmd.Flags().BoolVar(&rotate, "rotate-secrets", false, rotateUsage)
	return cmd
}

func newApplyCommand() *cobra.Command {
	var (
		f           projectFlags
		refresh     bool
		autoApprove bool
		rotate      bool
	)
	cmd := &cobra.Command{
		Use:   "apply -e <environment> [path...]",
		Short: "Deploy the configuration to an environment",
		Long: `Apply plans like plan does, asks for confirmation, and then makes the changes.
It holds the environment's lock throughout and records the result in state.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			set, err := loadProject(cmd, args)
			if err != nil {
				return err
			}
			e, err := newEngine(cmd, set, &f)
			if err != nil {
				return err
			}
			return run(cmd, e, f.env, "apply", engine.PlanOptions{Refresh: refresh, RotateSecrets: rotate}, autoApprove)
		},
	}
	f.register(cmd, true)
	registerLockFile(cmd, &f.lockFile)
	cmd.Flags().BoolVar(&refresh, "refresh", true, "ask the host for the current state of every agent first")
	cmd.Flags().BoolVar(&autoApprove, "auto-approve", false, "apply without asking for confirmation")
	cmd.Flags().BoolVar(&rotate, "rotate-secrets", false, rotateUsage)
	return cmd
}

// rotateUsage explains --rotate-secrets. State never sees secret values,
// so it can't tell when one changes (ADR 0016).
const rotateUsage = "send every host vault's credential values again, after changing them in the secret source"

func newDestroyCommand() *cobra.Command {
	var (
		f           projectFlags
		autoApprove bool
	)
	cmd := &cobra.Command{
		Use:   "destroy -e <environment> [path...]",
		Short: "Remove every agent Solaria deployed to an environment",
		Long: `Destroy removes, or on some hosts archives, every agent recorded in the
environment's state, then empties the state. It works even after the
environment was removed from the configuration.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			set, err := loadProject(cmd, args)
			if err != nil {
				return err
			}
			e, err := newEngine(cmd, set, &f)
			if err != nil {
				return err
			}
			return run(cmd, e, f.env, "destroy", engine.PlanOptions{Refresh: true, Destroy: true}, autoApprove)
		},
	}
	f.register(cmd, true)
	cmd.Flags().BoolVar(&autoApprove, "auto-approve", false, "destroy without asking for confirmation")
	return cmd
}

// makePlan plans and prints the plan's diagnostics, or the errors that
// stopped it.
func makePlan(cmd *cobra.Command, e *engine.Engine, env string, opts engine.PlanOptions) (*engine.Plan, error) {
	plan, err := e.Plan(context.Background(), env, opts)
	var failed *engine.FailedError
	if errors.As(err, &failed) {
		if perr := printDiagnostics(cmd.ErrOrStderr(), failed.Diagnostics); perr != nil {
			return nil, perr
		}
		return nil, err
	}
	if err != nil {
		return nil, err
	}
	return plan, printDiagnostics(cmd.ErrOrStderr(), plan.Diagnostics)
}

// run locks the environment, plans, confirms and applies.
func run(cmd *cobra.Command, e *engine.Engine, env, operation string, opts engine.PlanOptions, autoApprove bool) (err error) {
	unlock, err := e.Lock(env, operation)
	if err != nil {
		return err
	}
	defer func() {
		if uerr := unlock(); uerr != nil {
			err = errors.Join(err, fmt.Errorf("release lock: %w", uerr))
		}
	}()

	plan, err := makePlan(cmd, e, env, opts)
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	if err := engine.Render(out, plan); err != nil {
		return err
	}
	if !plan.HasChanges() {
		return nil
	}
	if !autoApprove {
		question := fmt.Sprintf("\nApply these changes to %s? Only 'yes' is accepted: ", env)
		if opts.Destroy {
			question = fmt.Sprintf("\nDestroy everything Solaria deployed to %s? Only 'yes' is accepted: ", env)
		}
		ok, err := confirm(cmd.InOrStdin(), out, question)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("%s cancelled", operation)
		}
	}
	applyErr := e.Apply(context.Background(), plan)
	summary(out, titleCase(operation), plan, applyErr)
	return applyErr
}

func confirm(in io.Reader, out io.Writer, prompt string) (bool, error) {
	if _, err := io.WriteString(out, prompt); err != nil {
		return false, err
	}
	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false, err
	}
	if _, err := io.WriteString(out, "\n"); err != nil {
		return false, err
	}
	return strings.TrimSpace(line) == "yes", nil
}

func summary(w io.Writer, operation string, plan *engine.Plan, applyErr error) {
	var b strings.Builder
	if applyErr != nil {
		fmt.Fprintf(&b, "\n%s stopped. Steps completed before the error are recorded in state (serial %d).\n", operation, plan.Serial())
	} else {
		c, u, r, d := plan.Counts()
		fmt.Fprintf(&b, "\n%s complete: %d created, %d updated, %d replaced, %d deleted. State serial %d.\n", operation, c, u, r, d, plan.Serial())
	}
	if h := plan.HostEntry(); h != nil && plan.Host != nil && plan.Host.Action != provider.NoOp && plan.Host.Action != provider.Delete {
		fmt.Fprintf(&b, "  %s: %s\n", h.ID, h.Binding.HostID)
	}
	for _, s := range plan.Steps {
		if s.Action == provider.NoOp || s.Action == provider.Delete || s.Desired == nil {
			continue
		}
		if e := plan.Entry(s.ID); e != nil && e.ResolvedDigest == s.Desired.ResolvedDigest {
			fmt.Fprintf(&b, "  %s: %s", s.ID, e.Binding.HostID)
			if e.Binding.Version != "" {
				fmt.Fprintf(&b, " version %s", e.Binding.Version)
			}
			fmt.Fprintf(&b, " (%s)\n", provider.ShortDigest(e.ResolvedDigest))
			// A provider's run output is how to start the agent.
			if run := e.Binding.Outputs["run"]; run != "" {
				fmt.Fprintf(&b, "    run: %s\n", run)
			}
		}
	}
	_, _ = io.WriteString(w, b.String())
}

func titleCase(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
