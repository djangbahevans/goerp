package cli

import (
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/djangbahevans/goerp/internal/cli/admin"
	"github.com/djangbahevans/goerp/internal/cli/clierr"
	"github.com/djangbahevans/goerp/internal/cli/events"
	"github.com/djangbahevans/goerp/internal/cli/jobs"
	"github.com/djangbahevans/goerp/internal/cli/module"
	"github.com/djangbahevans/goerp/internal/cli/schema"
	"github.com/djangbahevans/goerp/internal/cli/tenant"
	"github.com/spf13/cobra"
)

func Execute() int {
	rootCmd := newRootCmd()
	cmd, err := rootCmd.ExecuteC()
	if err == nil {
		return 0
	}

	fmt.Fprintln(os.Stderr, "Error:", err)

	if ec, ok := errors.AsType[clierr.ExitCoder](err); ok {
		if ec.ExitCode() == 2 {
			fmt.Fprintln(os.Stderr)
			_ = cmd.Usage()
		}
		return ec.ExitCode()
	}

	return 1
}

func newRootCmd() *cobra.Command {
	var json, quiet, yes bool
	var env, adminURL, adminToken, clientCert, clientKey, caCert string
	var timeout time.Duration

	rootCmd := &cobra.Command{
		Use:           "goerp",
		Short:         "goerp manages GoERP modules and platform operations",
		SilenceErrors: true,
		SilenceUsage:  true,
	}

	rootCmd.SetFlagErrorFunc(func(cmd *cobra.Command, err error) error {
		return clierr.Usage(err)
	})

	rootCmd.AddCommand(admin.NewCmd())
	rootCmd.AddCommand(events.NewCmd())
	rootCmd.AddCommand(jobs.NewCmd())
	rootCmd.AddCommand(module.NewCmd())
	rootCmd.AddCommand(schema.NewCmd())
	rootCmd.AddCommand(tenant.NewCmd())

	rootCmd.PersistentFlags().StringVar(&env, "env", "dev", "Named environment: dev | staging | production")
	rootCmd.PersistentFlags().StringVar(&adminURL, "admin-url", "", "Admin API base URL — loopback (http://localhost:8081) in local dev, or the admin gateway's HTTPS address in staging/production (see §1a)")
	rootCmd.PersistentFlags().StringVar(&adminToken, "admin-token", "", "Superadmin bearer token")
	rootCmd.PersistentFlags().StringVar(&clientCert, "client-cert", "", "Operator mTLS client certificate path — required when --admin-url is not a loopback address")
	rootCmd.PersistentFlags().StringVar(&clientKey, "client-key", "", "Operator mTLS client key path — required when --admin-url is not a loopback address")
	rootCmd.PersistentFlags().StringVar(&caCert, "ca-cert", "", "CA certificate to verify the admin gateway's server certificate (optional if it's signed by a publicly trusted CA)")

	rootCmd.PersistentFlags().DurationVar(&timeout, "timeout", 30*time.Second, "Request timeout for API calls")

	rootCmd.PersistentFlags().BoolVar(&json, "json", false, "Output results as JSON instead of human readable text")
	rootCmd.PersistentFlags().BoolVar(&quiet, "quiet", false, "Suppress informational output; only print errors and results")
	rootCmd.PersistentFlags().BoolVar(&yes, "yes", false, "Answer \"yes\" to any broad-target confirmation prompt — implied when --json is set")

	return rootCmd
}
