package module

import (
	"fmt"

	"github.com/djangbahevans/goerp/internal/cli/clierr"
	internalmodule "github.com/djangbahevans/goerp/internal/module"
	"github.com/spf13/cobra"
)

func newBuildCmd() *cobra.Command {
	var output string
	var noWasm, noFrontend, debug bool

	cmd := &cobra.Command{
		Use:   "build [path]",
		Short: "Compile a module to a .erp package",
		Args:  clierr.WrapArgs(cobra.MaximumNArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := "."
			if len(args) > 0 {
				dir = args[0]
			}

			result, err := internalmodule.Package(cmd.Context(), dir, internalmodule.PackageOptions{
				Output:       output,
				SkipWasm:     noWasm,
				SkipFrontend: noFrontend,
				Debug:        debug,
			})
			if err != nil {
				return err
			}

			_, err = fmt.Fprintf(cmd.OutOrStdout(), "built %s (%s)\n", result.ArchivePath, result.ArchiveSHA256)
			return err
		},
	}

	cmd.Flags().StringVar(&output, "output", "", "Output path for the .erp file (default: build/<name>-<version>.erp)")
	cmd.Flags().BoolVar(&noWasm, "no-wasm", false, "Skip WASM compilation (frontend iteration only)")
	cmd.Flags().BoolVar(&noFrontend, "no-frontend", false, "Skip frontend bundle compilation (Go/handler iteration only)")
	cmd.Flags().BoolVar(&debug, "debug", false, "Include debug info and source maps (larger output, better errors)")

	return cmd
}
