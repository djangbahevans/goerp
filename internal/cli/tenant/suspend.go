package tenant

import (
	"fmt"

	"github.com/djangbahevans/goerp/internal/cli/adminclient"
	"github.com/djangbahevans/goerp/internal/cli/clierr"
	"github.com/spf13/cobra"
)

func newSuspendCmd() *cobra.Command {
	var reason string

	cmd := &cobra.Command{
		Use:   "suspend <slug>",
		Short: "Suspend a tenant — blocks new logins and terminates active sessions immediately; data is preserved",
		Args:  clierr.WrapArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			slug := args[0]

			if reason == "" {
				return clierr.Usage(fmt.Errorf("--reason is required"))
			}

			client, err := adminclient.NewFromFlags(cmd)
			if err != nil {
				return err
			}

			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "suspending tenant %q...\n", slug)

			data, err := client.Post(cmd.Context(),
				fmt.Sprintf("/admin/tenants/%s/suspend", slug),
				map[string]string{"reason": reason},
			)

			jsonOut, _ := cmd.Flags().GetBool("json")
			if err != nil {
				return adminclient.WithJSONErrorEnvelope(cmd, err, jsonOut)
			}

			if jsonOut {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), string(data))
			} else {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "tenant %q suspended\n", slug)
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&reason, "reason", "", "Reason for suspension, recorded in the audit log (required)")

	return cmd
}
