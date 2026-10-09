package module

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/djangbahevans/goerp/internal/cli/adminclient"
	"github.com/djangbahevans/goerp/internal/cli/clierr"
	internalmodule "github.com/djangbahevans/goerp/internal/module"
	"github.com/spf13/cobra"
)

func newBuildCmd() *cobra.Command {
	var output, workerStorage, reloadURL, reloadToken string
	var noWasm, noFrontend, noWorker, debug, watch bool

	cmd := &cobra.Command{
		Use:   "build [path]",
		Short: "Compile a module to a .erp package",
		Args:  clierr.WrapArgs(cobra.MaximumNArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := "."
			if len(args) > 0 {
				dir = args[0]
			}

			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()

			opts := internalmodule.PackageOptions{
				Output:           output,
				SkipWasm:         noWasm,
				SkipFrontend:     noFrontend,
				SkipWorker:       noWorker,
				WorkerStorageDir: workerStorage,
				Debug:            debug,
			}
			var client *adminclient.Client
			if reloadURL != "" || reloadToken != "" {
				var err error
				client, err = adminclient.New(reloadURL, reloadToken, 2*time.Minute)
				if err != nil {
					return err
				}
			}

			build := func(ctx context.Context) error {
				result, err := internalmodule.Package(ctx, dir, opts)
				if err != nil {
					return err
				}

				if client != nil {
					data, err := os.ReadFile(result.ArchivePath)
					if err != nil {
						return err
					}
					if _, err := client.PostBinary(ctx, "/admin/dev/modules/"+result.Name+"/reload", data); err != nil {
						return fmt.Errorf("reload module: %w", err)
					}
				}

				_, err = fmt.Fprintf(cmd.OutOrStdout(), "built %s (%s)\n", result.ArchivePath, result.ArchiveSHA256)
				return err
			}
			if watch {
				return internalmodule.Watch(ctx, dir, cmd.ErrOrStderr(), build)
			}

			return build(ctx)
		},
	}

	cmd.Flags().StringVar(&output, "output", "", "Output path for the .erp file (default: build/<name>-<version>.erp)")
	cmd.Flags().BoolVar(&noWasm, "no-wasm", false, "Skip WASM compilation (frontend iteration only)")
	cmd.Flags().BoolVar(&noFrontend, "no-frontend", false, "Skip frontend bundle compilation (Go/handler iteration only)")
	cmd.Flags().BoolVar(&debug, "debug", false, "Include debug info and source maps (larger output, better errors)")
	cmd.Flags().BoolVar(&watch, "watch", false, "Rebuild on source changes; failed builds keep the last successful package")
	cmd.Flags().BoolVar(&noWorker, "no-worker", false, "Skip workflow-worker compilation")
	cmd.Flags().StringVar(&workerStorage, "worker-storage", "", "Stage workflow-worker binaries in the local development storage directory")
	cmd.Flags().StringVar(&reloadURL, "reload-url", "", "Development engine admin URL to reload after a successful build")
	cmd.Flags().StringVar(&reloadToken, "reload-token", os.Getenv("GOERP_MODULE_RELOAD_TOKEN"), "Development engine admin bearer token")

	return cmd
}
