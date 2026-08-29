package tenant

import (
	"fmt"

	"github.com/djangbahevans/goerp/internal/cli/adminclient"
	"github.com/djangbahevans/goerp/internal/cli/clierr"
	"github.com/spf13/cobra"
)

func newUnsuspendCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "unsuspend <slug>",
		Short: "Restore a suspended tenant to active status",
		Args:  clierr.WrapArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			slug := args[0]

			client, err := adminclient.NewFromFlags(cmd)
			if err != nil {
				return err
			}

			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "unsuspending tenant %q...\n", slug)

			data, err := client.Post(cmd.Context(), fmt.Sprintf("/admin/tenants/%s/unsuspend", slug), nil)

			jsonOut, _ := cmd.Flags().GetBool("json")
			if err != nil {
				return adminclient.WithJSONErrorEnvelope(cmd, err, jsonOut)
			}

			if jsonOut {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), string(data))
			} else {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "tenant %q unsuspended\n", slug)
			}

			return nil
		},
	}

	return cmd
}
