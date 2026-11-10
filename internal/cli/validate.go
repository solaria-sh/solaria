package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/solaria-sh/solaria/internal/api/v1alpha1"
	"github.com/solaria-sh/solaria/internal/config"
	"github.com/solaria-sh/solaria/internal/diag"
	"github.com/solaria-sh/solaria/internal/lockfile"
	"github.com/solaria-sh/solaria/internal/validate"
)

func newValidateCommand() *cobra.Command {
	var output, lockPath string
	cmd := &cobra.Command{
		Use:   "validate [path...]",
		Short: "Check resources for errors",
		Long: `Validate loads every Solaria resource under the given files and directories
(the current directory by default) and checks it against the schema, its
references and the files it names. It also checks the lock file, when there
is one. It doesn't contact any host.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if output != "text" && output != "json" {
				return fmt.Errorf("--output must be text or json, not %q", output)
			}
			if len(args) == 0 {
				args = []string{"."}
			}
			set, diags := check(args, lockPath)

			write := writeText
			if output == "json" {
				write = writeJSON
			}
			if err := write(cmd.OutOrStdout(), set, diags); err != nil {
				return err
			}
			if n, _ := diags.Count(); n > 0 {
				return fmt.Errorf("validation failed with %s", plural(n, "error"))
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&output, "output", "o", "text", "output format: text or json")
	registerLockFile(cmd, &lockPath)
	return cmd
}

// check runs everything validate checks on the resources under paths: the
// schema, references, the files they name, and the lock file at lockPath.
// The diagnostics come back sorted.
func check(paths []string, lockPath string) (*config.Set, diag.List) {
	set, diags := config.Load(paths)
	if set != nil {
		diags = append(diags, validate.Validate(set)...)
		lock, err := lockfile.Read(lockPath)
		if err != nil {
			diags = append(diags, diag.Errorf("", diag.Pos{}, "%v", err))
		} else {
			diags = append(diags, lock.Validate(lockPath, set)...)
		}
		if len(set.Resources) == 0 && !diags.HasErrors() {
			diags = append(diags, diag.Errorf("", diag.Pos{},
				"no %s resources found in %s", v1alpha1.Group, strings.Join(paths, ", ")))
		}
	}
	diags.Sort()
	return set, diags
}

func writeText(w io.Writer, set *config.Set, diags diag.List) error {
	var b strings.Builder
	for _, d := range diags {
		b.WriteString(d.String() + "\n")
	}
	errs, warns := diags.Count()
	if errs == 0 && set != nil && len(set.Resources) > 0 {
		b.WriteString("valid: " + countKinds(set))
		if warns > 0 {
			b.WriteString(" (" + plural(warns, "warning") + ")")
		}
		b.WriteString("\n")
	}
	_, err := io.WriteString(w, b.String())
	return err
}

type jsonResult struct {
	Valid       bool              `json:"valid"`
	Resources   []jsonResource    `json:"resources"`
	Diagnostics []diag.Diagnostic `json:"diagnostics"`
}

type jsonResource struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
	File string `json:"file"`
}

func writeJSON(w io.Writer, set *config.Set, diags diag.List) error {
	res := jsonResult{Valid: !diags.HasErrors(), Resources: []jsonResource{}, Diagnostics: diags}
	if res.Diagnostics == nil {
		res.Diagnostics = diag.List{}
	}
	if set != nil {
		for _, r := range set.Resources {
			res.Resources = append(res.Resources, jsonResource{r.Kind, r.Name, r.File})
		}
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(res)
}

func countKinds(set *config.Set) string {
	var parts []string
	for _, k := range v1alpha1.Kinds {
		if n := len(set.Kind(k)); n > 0 {
			parts = append(parts, plural(n, k))
		}
	}
	return strings.Join(parts, ", ")
}

func plural(n int, word string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", word)
	}
	return fmt.Sprintf("%d %ss", n, word)
}
