package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/user"
	"slices"

	"github.com/spf13/cobra"

	"github.com/solaria-sh/solaria/internal/api/v1alpha1"
	"github.com/solaria-sh/solaria/internal/config"
	"github.com/solaria-sh/solaria/internal/diag"
	"github.com/solaria-sh/solaria/internal/engine"
	"github.com/solaria-sh/solaria/internal/gitinfo"
	"github.com/solaria-sh/solaria/internal/lockfile"
	"github.com/solaria-sh/solaria/internal/providers"
	"github.com/solaria-sh/solaria/internal/state"
	"github.com/solaria-sh/solaria/internal/validate"
)

// defaultStateDir is where local state lives, relative to the working
// directory.
const defaultStateDir = ".solaria/state"

// projectFlags are shared by the commands that work on a target.
type projectFlags struct {
	env      string
	stateDir string
	// lockFile is empty for commands that ignore the lock, such as destroy.
	lockFile string
}

func (f *projectFlags) register(cmd *cobra.Command, envRequired bool) {
	cmd.Flags().StringVarP(&f.env, "target", "t", "", "target to work on")
	cmd.Flags().StringVar(&f.stateDir, "state-dir", defaultStateDir, "directory holding local state")
	if envRequired {
		_ = cmd.MarkFlagRequired("target")
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

func newEngine(cmd *cobra.Command, set *config.Set, f *projectFlags) (*engine.Engine, error) {
	var lock *lockfile.File
	if f.lockFile != "" {
		var err error
		if lock, err = lockfile.Read(f.lockFile); err != nil {
			return nil, err
		}
	}
	backend, err := stateBackend(set, f.env, f.stateDir)
	if err != nil {
		return nil, err
	}
	return &engine.Engine{
		Log:          cmd.ErrOrStderr(),
		Set:          set,
		Backend:      backend,
		NewProvider:  providers.New,
		WorkDir:      ".",
		Source:       gitinfo.Source("."),
		Who:          whoami(),
		LockFile:     lock,
		LockFilePath: f.lockFile,
	}, nil
}

// stateBackend returns where env's state is kept: the backend its
// Target names in spec.state (ADR 0009), or local files in stateDir.
// A target that has left the configuration has local state.
func stateBackend(set *config.Set, env, stateDir string) (state.Backend, error) {
	if a := remoteState(set, env); a != nil {
		return state.NewAzureBlob(state.AzureBlobConfig{Account: a.Account, Container: a.Container, Prefix: a.Prefix})
	}
	return &state.Local{Dir: stateDir}, nil
}

// remoteState returns the Azure Blob container env's state is kept in, or
// nil for local state.
func remoteState(set *config.Set, env string) *v1alpha1.AzureBlobState {
	if set == nil {
		return nil
	}
	r := set.Lookup(v1alpha1.KindTarget, env)
	if r == nil || r.Target().Spec.State == nil {
		return nil
	}
	return r.Target().Spec.State.AzureBlob
}

// targetsWithState lists the targets that have state: those with
// local state files, except targets the configuration keeps
// elsewhere, and those whose remote state exists.
func targetsWithState(set *config.Set, stateDir string) ([]string, error) {
	local, err := (&state.Local{Dir: stateDir}).Targets()
	if err != nil {
		return nil, err
	}
	var envs []string
	for _, env := range local {
		if remoteState(set, env) == nil {
			envs = append(envs, env)
		}
	}
	for _, r := range set.Kind(v1alpha1.KindTarget) {
		if remoteState(set, r.Name) == nil {
			continue
		}
		backend, err := stateBackend(set, r.Name, stateDir)
		if err != nil {
			return nil, err
		}
		st, err := backend.Read(r.Name)
		if err != nil {
			return nil, err
		}
		if st != nil {
			envs = append(envs, r.Name)
		}
	}
	slices.Sort(envs)
	return envs, nil
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
