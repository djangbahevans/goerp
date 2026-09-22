package module

import (
	"fmt"
	"strings"

	"github.com/djangbahevans/goerp/internal/cli/clierr"
	internalmodule "github.com/djangbahevans/goerp/internal/module"
	"github.com/spf13/cobra"
)

func newGenerateCmd() *cobra.Command {
	var check bool

	cmd := &cobra.Command{
		Use:   "generate [path]",
		Short: "Generate models/*.gen.go from a module's schema package",
		Args:  clierr.WrapArgs(cobra.MaximumNArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := "."
			if len(args) > 0 {
				dir = args[0]
			}

			result, err := internalmodule.Generate(cmd.Context(), dir, internalmodule.GenerateOptions{Check: check})
			if err != nil {
				return err
			}

			if len(result.Stale) == 0 {
				_, err := fmt.Fprintln(cmd.OutOrStdout(), "models/ is up to date")
				return err
			}

			_, err = fmt.Fprintf(cmd.OutOrStdout(), "wrote %d file(s): %s\n", len(result.Stale), strings.Join(result.Stale, ", "))
			return err
		},
	}

	cmd.Flags().BoolVar(&check, "check", false, "Check whether models/ is up to date without writing")

	return cmd
}
