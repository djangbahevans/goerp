package module

import (
	"fmt"

	"github.com/djangbahevans/goerp/internal/cli/clierr"
	internalmodule "github.com/djangbahevans/goerp/internal/module"
	"github.com/spf13/cobra"
)

func newCreateCmd() *cobra.Command {
	var moduleType, dir, org string

	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Scaffold a new module",
		Args:  clierr.WrapArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			targetDir := dir
			if targetDir == "" {
				targetDir = name
			}

			if err := internalmodule.Create(targetDir, name, moduleType, org); err != nil {
				return err
			}

			fmt.Fprintf(cmd.OutOrStdout(), "created module %q in %s\n", name, targetDir)
			return nil
		},
	}

	cmd.Flags().StringVar(&moduleType, "type", "domain", "Module type: domain | connector | l10n | extension")
	cmd.Flags().StringVar(&dir, "dir", "", "Directory to create the module in (default: ./<name>)")
	cmd.Flags().StringVar(&org, "org", "", "Go module path prefix (e.g. github.com/yourorg)")

	return cmd
}
