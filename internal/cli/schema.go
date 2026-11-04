package cli

import (
	"fmt"
	"io/fs"
	"strings"

	"github.com/spf13/cobra"

	"github.com/renantarouco/solaria/internal/api/v1alpha1"
	"github.com/renantarouco/solaria/schemas"
)

func newSchemaCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "schema [kind]",
		Short: "Print the JSON Schema for resources",
		Long: `Schema prints the JSON Schema that validate uses. With no argument it prints
the schema for any resource, which dispatches on kind; editors should use that
one. With a kind (` + strings.Join(v1alpha1.Kinds, ", ") + `) it prints that kind's schema.`,
		Args:      cobra.MaximumNArgs(1),
		ValidArgs: v1alpha1.Kinds,
		RunE: func(cmd *cobra.Command, args []string) error {
			file := schemas.CombinedFile
			if len(args) == 1 {
				kind := ""
				for _, k := range v1alpha1.Kinds {
					if strings.EqualFold(k, args[0]) {
						kind = k
					}
				}
				if kind == "" {
					return fmt.Errorf("unknown kind %q; %s has %s", args[0], v1alpha1.APIVersion, strings.Join(v1alpha1.Kinds, ", "))
				}
				file = schemas.File(kind)
			}
			b, err := fs.ReadFile(schemas.V1alpha1(), file)
			if err != nil {
				return err
			}
			_, err = cmd.OutOrStdout().Write(b)
			return err
		},
	}
}
