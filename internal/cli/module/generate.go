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
		Short: "Generate models/*.gen.go and the manifest's generated blocks",
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

			if len(result.Stale) == 0 && len(result.Blocks) == 0 {
				_, err := fmt.Fprintln(cmd.OutOrStdout(), "generated output is up to date")
				return err
			}

			if len(result.Stale) > 0 {
				if _, err := fmt.Fprintf(cmd.OutOrStdout(), "wrote %d file(s): %s\n", len(result.Stale), strings.Join(result.Stale, ", ")); err != nil {
					return err
				}
			}
			if len(result.Blocks) > 0 {
				_, err = fmt.Fprintf(cmd.OutOrStdout(), "updated manifest.json keys: %s\n", strings.Join(result.Blocks, ", "))
			}
			return err
		},
	}

	cmd.Flags().BoolVar(&check, "check", false, "Check whether models/ and the manifest's generated blocks are up to date without writing")

	return cmd
}
