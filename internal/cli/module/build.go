package module

import (
	"fmt"

	"github.com/djangbahevans/goerp/internal/cli/clierr"
	internalmodule "github.com/djangbahevans/goerp/internal/module"
	"github.com/spf13/cobra"
)

func newBuildCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "build <path>",
		Short: "Build a module's frontend bundle",
		Args:  clierr.WrapArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := args[0]

			result, err := internalmodule.BuildFrontend(cmd.Context(), dir)
			if err != nil {
				return err
			}

			if result == nil {
				_, err := fmt.Fprintln(cmd.OutOrStdout(), "no frontend bundle declared; nothing to build")
				return err
			}

			_, err = fmt.Fprintf(cmd.OutOrStdout(), "built %s (%s)\n", result.BundlePath, result.BundleSHA256)
			return err
		},
	}

	return cmd
}
