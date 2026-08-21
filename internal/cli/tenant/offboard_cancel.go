package tenant

import (
	"fmt"

	"github.com/djangbahevans/goerp/internal/cli/adminclient"
	"github.com/djangbahevans/goerp/internal/cli/clierr"
	"github.com/spf13/cobra"
)

// newOffboardCancelCmd is Tier 1 (cli-reference.md §2a) — no --confirm.
// Cancelling a still-cancellable offboard just reverses a status flag and
// cancels a not-yet-run deletion, the same low-stakes shape as unsuspend.
func newOffboardCancelCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cancel <slug>",
		Short: "Cancel a pending tenant offboard while it's still in its grace period",
		Args:  clierr.WrapArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			slug := args[0]

			client, err := adminclient.NewFromFlags(cmd)
			if err != nil {
				return err
			}

			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "cancelling offboard for tenant %q...\n", slug)

			data, err := client.Post(cmd.Context(), fmt.Sprintf("/admin/tenants/%s/offboard/cancel", slug), nil)

			jsonOut, _ := cmd.Flags().GetBool("json")
			if err != nil {
				if jsonOut {
					if envJSON, ok := adminclient.ErrorEnvelopeJSON(err); ok {
						_, _ = fmt.Fprintln(cmd.OutOrStdout(), string(envJSON))
					}
				}
				return err
			}

			if jsonOut {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), string(data))
			} else {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "tenant %q offboard cancelled, restored to active\n", slug)
			}

			return nil
		},
	}

	return cmd
}
