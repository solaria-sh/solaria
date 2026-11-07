package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/user"

	"github.com/spf13/cobra"

	"github.com/solaria-sh/solaria/internal/config"
	"github.com/solaria-sh/solaria/internal/diag"
	"github.com/solaria-sh/solaria/internal/engine"
	"github.com/solaria-sh/solaria/internal/gitinfo"
	"github.com/solaria-sh/solaria/internal/providers"
	"github.com/solaria-sh/solaria/internal/state"
	"github.com/solaria-sh/solaria/internal/validate"
)

// defaultStateDir is where local state lives, relative to the working
// directory.
const defaultStateDir = ".solaria/state"

// projectFlags are shared by the commands that work on an environment.
type projectFlags struct {
	env      string
	stateDir string
}

func (f *projectFlags) register(cmd *cobra.Command, envRequired bool) {
	cmd.Flags().StringVarP(&f.env, "environment", "e", "", "environment to work on")
	cmd.Flags().StringVar(&f.stateDir, "state-dir", defaultStateDir, "directory holding local state")
	if envRequired {
		_ = cmd.MarkFlagRequired("environment")
	}
}

// loadProject loads and validates the resources under paths (the current
// directory by default). It prints diagnostics to stderr and fails on
// errors, since nothing should be planned from an invalid configuration.
func loadProject(cmd *cobra.Command, paths []string) (*config.Set, error) {
	if len(paths) == 0 {
		paths = []string{"."}
	}
	set, diags := config.Load(paths)
	if set != nil {
		diags = append(diags, validate.Validate(set)...)
	}
	diags.Sort()
	if err := printDiagnostics(cmd.ErrOrStderr(), diags); err != nil {
		return nil, err
	}
	if n, _ := diags.Count(); n > 0 {
		return nil, fmt.Errorf("configuration has %s; run solaria validate for details", plural(n, "error"))
	}
	return set, nil
}

func printDiagnostics(w io.Writer, diags diag.List) error {
	for _, d := range diags {
		if _, err := fmt.Fprintln(w, d); err != nil {
			return err
		}
	}
	return nil
}

func newEngine(cmd *cobra.Command, set *config.Set, f *projectFlags) *engine.Engine {
	return &engine.Engine{
		Log:         cmd.ErrOrStderr(),
		Set:         set,
		Backend:     &state.Local{Dir: f.stateDir},
		NewProvider: providers.New,
		WorkDir:     ".",
		Source:      gitinfo.Source("."),
		Who:         whoami(),
	}
}

func whoami() string {
	if u, err := user.Current(); err == nil && u.Username != "" {
		return u.Username
	}
	return os.Getenv("USER")
}

// exitError ends the command with a specific exit code and no message.
type exitError struct{ code int }

func (e *exitError) Error() string { return fmt.Sprintf("exit status %d", e.code) }

// exitCode returns the code for an error returned by a command.
func exitCode(err error) (code int, quiet bool) {
	var ee *exitError
	if errors.As(err, &ee) {
		return ee.code, true
	}
	return 1, false
}
