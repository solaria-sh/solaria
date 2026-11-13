package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
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
		output   string
		out      string
	)
	cmd := &cobra.Command{
		Use:   "plan -t <target> [path...]",
		Short: "Show what apply would change",
		Long: `Plan resolves every agent the target receives, checks it against the
provider's capabilities, refreshes the state from the host, and shows what
apply would create, update, replace or delete. It changes nothing.

-o json prints the plan in a documented JSON format instead. --out saves
that JSON to a file, which apply <file> applies only if nothing changed
since (ADR 0018).`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if output != "text" && output != "json" {
				return fmt.Errorf("--output must be text or json, not %q", output)
			}
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
			doc := e.Document(plan)
			w := cmd.OutOrStdout()
			if output == "json" {
				if err := encodeJSON(w, doc); err != nil {
					return err
				}
			} else if err := engine.Render(w, plan); err != nil {
				return err
			}
			if out != "" {
				if err := savePlan(out, doc); err != nil {
					return err
				}
				if output == "text" {
					if _, err := fmt.Fprintf(w, "\nSaved the plan to %s. Apply exactly this plan with: solaria apply %s\n", out, out); err != nil {
						return err
					}
				}
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
	cmd.Flags().BoolVar(&destroy, "destroy", false, "plan the removal of everything in the target")
	cmd.Flags().BoolVar(&rotate, "rotate-secrets", false, rotateUsage)
	cmd.Flags().StringVarP(&output, "output", "o", "text", "output format: text or json")
	cmd.Flags().StringVar(&out, "out", "", "also save the plan to this file, for apply <file>")
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
		Use:   "apply (-t <target> | <plan-file>) [path...]",
		Short: "Deploy the configuration to a target",
		Long: `Apply plans like plan does, asks for confirmation, and then makes the changes.
It holds the target's lock throughout and records the result in state.

Given a file saved by plan --out instead of -t, apply plans again with the
plan's options and applies without asking, but only if the result matches
the file: the same state serial, Solaria version, and every step's action
and digests (ADR 0018). Otherwise it changes nothing and says what differs.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			saved, err := savedPlan(args)
			if err != nil {
				return err
			}
			var file string
			opts := engine.PlanOptions{Refresh: refresh, RotateSecrets: rotate}
			switch {
			case saved != nil && f.env != "":
				return fmt.Errorf("%s names its target, %s; leave out -t", args[0], saved.Target)
			case saved != nil:
				for _, flag := range []string{"refresh", "rotate-secrets"} {
					if cmd.Flags().Changed(flag) {
						return fmt.Errorf("--%s comes from the plan in %s; leave it out", flag, args[0])
					}
				}
				file, f.env, args = args[0], saved.Target, args[1:]
				opts = engine.PlanOptions(saved.Options)
			case f.env == "":
				return errors.New(`required flag(s) "target" not set`)
			}
			set, err := loadProject(cmd, args)
			if err != nil {
				return err
			}
			e, err := newEngine(cmd, set, &f)
			if err != nil {
				return err
			}
			if saved != nil {
				operation := "apply"
				if opts.Destroy {
					operation = "destroy"
				}
				return runSaved(cmd, e, operation, opts, saved, file)
			}
			return run(cmd, e, f.env, "apply", opts, autoApprove)
		},
	}
	f.register(cmd, false)
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
		Use:   "destroy -t <target> [path...]",
		Short: "Remove every agent Solaria deployed to a target",
		Long: `Destroy removes, or on some hosts archives, every agent recorded in the
target's state, then empties the state. It works even after the
target was removed from the configuration.`,
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

// run locks the target, plans, confirms and applies.
func run(cmd *cobra.Command, e *engine.Engine, env, operation string, opts engine.PlanOptions, autoApprove bool) error {
	return lockAndApply(cmd, e, env, operation, opts, autoApprove, nil, "")
}

// runSaved applies a plan saved by plan --out: it plans again under the
// lock and applies, without asking, only what the saved plan showed.
func runSaved(cmd *cobra.Command, e *engine.Engine, operation string, opts engine.PlanOptions, saved *engine.Document, file string) error {
	return lockAndApply(cmd, e, saved.Target, operation, opts, true, saved, file)
}

func lockAndApply(cmd *cobra.Command, e *engine.Engine, env, operation string, opts engine.PlanOptions, autoApprove bool, saved *engine.Document, file string) (err error) {
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
	if saved != nil {
		if diffs := saved.Differences(e.Document(plan)); len(diffs) > 0 {
			return &stalePlanError{file: file, env: env, diffs: diffs}
		}
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

// savedPlan reads the plan file apply was given as its first argument, or
// returns nil when the first argument isn't one: a plan file is JSON with
// a solaria.sh/plan/ format, which no configuration file has.
func savedPlan(args []string) (*engine.Document, error) {
	if len(args) == 0 {
		return nil, nil
	}
	b, err := os.ReadFile(args[0])
	if err != nil {
		return nil, nil // a directory, or not there: a project path
	}
	var head struct {
		Format string `json:"format"`
	}
	if json.Unmarshal(b, &head) != nil || !strings.HasPrefix(head.Format, "solaria.sh/plan/") {
		return nil, nil
	}
	if head.Format != engine.PlanFormat {
		return nil, fmt.Errorf("%s: plan format %s isn't one this Solaria reads; it reads %s", args[0], head.Format, engine.PlanFormat)
	}
	var doc engine.Document
	if err := json.Unmarshal(b, &doc); err != nil {
		return nil, fmt.Errorf("%s: %w", args[0], err)
	}
	if doc.Target == "" {
		return nil, fmt.Errorf("%s: the plan names no target", args[0])
	}
	return &doc, nil
}

// savePlan writes the plan document for apply <file>.
func savePlan(path string, doc *engine.Document) error {
	var b strings.Builder
	if err := encodeJSON(&b, doc); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		return fmt.Errorf("save the plan: %w", err)
	}
	return nil
}

func encodeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}

// stalePlanError is a saved plan that no longer matches what planning
// again finds.
type stalePlanError struct {
	file, env string
	diffs     []string
}

func (e *stalePlanError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s no longer matches %s, so nothing was applied:\n", e.file, e.env)
	for _, d := range e.diffs {
		fmt.Fprintf(&b, "  - %s\n", d)
	}
	fmt.Fprintf(&b, "Plan again with: solaria plan -t %s --out %s", e.env, e.file)
	return b.String()
}
