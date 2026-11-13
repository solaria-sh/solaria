package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/solaria-sh/solaria/internal/api/v1alpha1"
	"github.com/solaria-sh/solaria/internal/config"
	"github.com/solaria-sh/solaria/internal/scaffold"
)

func newInitCommand() *cobra.Command {
	var template string
	cmd := &cobra.Command{
		Use:   "init [dir]",
		Short: "Create a new project from a template",
		Long: `Init creates a Solaria project in dir (the current directory by default),
creating dir if it doesn't exist.

Without --template it writes the ` + scaffold.Default + ` template: one Agent, its instructions, and
two Targets, local (the stub provider, which needs nothing installed)
and local-sdk (the Claude Agent SDK as a local process).

--template takes the name of an embedded template (` + strings.Join(scaffold.Names(), ", ") + `), or a
git repository in the shape Terraform uses for module sources:

  <git URL>[//<subdir>][?ref=<tag, branch or commit>]

for example https://github.com/solaria-sh/solaria//examples/reviewer?ref=v0.5.0.
The git command fetches it with your own git credentials.

A template is copied as it is, except .git/ and .solaria/, and nothing in it
is run. Init checks the template as validate would first, and writes nothing
if it has errors or if any of its files already exists in dir. The
.gitignore is merged instead: init makes sure it ignores .solaria/.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := "."
			if len(args) == 1 {
				dir = args[0]
			}
			src, err := scaffold.Parse(template)
			if err != nil {
				return err
			}

			tmp, err := os.MkdirTemp("", "solaria-init-")
			if err != nil {
				return err
			}
			defer func() { _ = os.RemoveAll(tmp) }()
			root, commit, err := scaffold.Fetch(cmd.Context(), src, tmp)
			if err != nil {
				return err
			}
			files, err := scaffold.Files(root)
			if err != nil {
				return fmt.Errorf("template %s: %w", src, err)
			}

			// Check the template where it is, before writing anything.
			set, diags := check([]string{root}, filepath.Join(root, "solaria.lock"))
			for i := range diags {
				diags[i].File = relativeTo(root, diags[i].File)
			}
			var warnings strings.Builder
			for _, d := range diags {
				warnings.WriteString(d.String() + "\n")
			}
			if _, err := io.WriteString(cmd.ErrOrStderr(), warnings.String()); err != nil {
				return err
			}
			if n, _ := diags.Count(); n > 0 {
				return fmt.Errorf("template %s has %s; nothing was written", src, plural(n, "error"))
			}

			if conflicts := scaffold.Conflicts(files, dir); len(conflicts) > 0 {
				return fmt.Errorf("init would overwrite %s in %s; nothing was written:\n  %s",
					plural(len(conflicts), "file"), dir, strings.Join(conflicts, "\n  "))
			}
			res, err := scaffold.Write(root, files, dir)
			if err != nil {
				return fmt.Errorf("init %s: %w", dir, err)
			}

			from := "template " + src.String()
			if commit != "" {
				from += " at " + commit
			}
			var b strings.Builder
			fmt.Fprintf(&b, "Created %s in %s from %s:\n", plural(len(res.Written), "file"), dir, from)
			for _, f := range res.Written {
				b.WriteString("  " + f + "\n")
			}
			if res.GitignoreUpdated {
				b.WriteString("Updated " + scaffold.Gitignore + ".\n")
			}
			b.WriteString("\nNext:\n")
			if filepath.Clean(dir) != "." {
				b.WriteString("  cd " + dir + "\n")
			}
			b.WriteString("  solaria validate\n")
			if env := firstTarget(set); env != "" {
				b.WriteString("  solaria plan -t " + env + "\n")
				b.WriteString("  solaria apply -t " + env + "\n")
			}
			_, err = io.WriteString(cmd.OutOrStdout(), b.String())
			return err
		},
	}
	cmd.Flags().StringVarP(&template, "template", "t", scaffold.Default, "embedded template name, or git source <url>[//<subdir>][?ref=<ref>]")
	return cmd
}

// relativeTo rewrites a diagnostic's file, found under root, as a path
// inside the template.
func relativeTo(root, file string) string {
	if file == "" {
		return file
	}
	rel, err := filepath.Rel(root, filepath.FromSlash(file))
	if err != nil || strings.HasPrefix(rel, "..") {
		return file
	}
	return filepath.ToSlash(rel)
}

// firstTarget names the target to suggest trying first: local when
// there is one, or else the first one found.
func firstTarget(set *config.Set) string {
	if set == nil {
		return ""
	}
	envs := set.Kind(v1alpha1.KindTarget)
	for _, e := range envs {
		if e.Name == "local" {
			return e.Name
		}
	}
	if len(envs) > 0 {
		return envs[0].Name
	}
	return ""
}
