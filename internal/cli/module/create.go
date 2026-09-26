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

			sdkVersion := internalmodule.SDKVersion()
			if err := internalmodule.Create(targetDir, name, moduleType, org, sdkVersion); err != nil {
				return err
			}

			if _, err := fmt.Fprintf(cmd.OutOrStdout(), "created module %q in %s\n", name, targetDir); err != nil {
				return err
			}

			stderr := cmd.ErrOrStderr()
			if sdkVersion == "" {
				_, err := fmt.Fprintf(stderr, "this goerp is a development build, so the SDK version isn't pinned.\n%s", sdkNextStep(targetDir))
				return err
			}
			if err := internalmodule.Tidy(cmd.Context(), targetDir); err != nil {
				_, err := fmt.Fprintf(stderr, "could not resolve SDK %s (%v)\nif you were offline, run `go mod tidy` in %s; otherwise %s",
					sdkVersion, err, targetDir, sdkNextStep(targetDir))
				return err
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&moduleType, "type", "domain", "Module type: domain | connector | l10n | extension")
	cmd.Flags().StringVar(&dir, "dir", "", "Directory to create the module in (default: ./<name>)")
	cmd.Flags().StringVar(&org, "org", "", "Go module path prefix (e.g. github.com/yourorg)")

	return cmd
}

func sdkNextStep(dir string) string {
	return fmt.Sprintf("next: in %s, run `go get %s@latest`, or `go mod edit -replace %s=<path to a goerp checkout>`, then `go mod tidy`\n",
		dir, internalmodule.SDKModulePath, internalmodule.SDKModulePath)
}
