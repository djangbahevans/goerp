package tenant

import (
	"fmt"

	"github.com/djangbahevans/goerp/internal/cli/adminclient"
	"github.com/djangbahevans/goerp/internal/cli/clierr"
	"github.com/spf13/cobra"
)

func newResendInviteCmd() *cobra.Command {
	var email string

	cmd := &cobra.Command{
		Use:   "resend-invite <slug>",
		Short: "Resend a pending admin invite for a tenant",
		Args:  clierr.WrapArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			slug := args[0]

			if email == "" {
				return clierr.Usage(fmt.Errorf("--email is required"))
			}

			client, err := adminclient.NewFromFlags(cmd)
			if err != nil {
				return err
			}

			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "resending invite to %s for tenant %q...\n", email, slug)

			data, err := client.Post(cmd.Context(),
				fmt.Sprintf("/admin/tenants/%s/resend-invite", slug),
				map[string]string{"email": email},
			)

			jsonOut, _ := cmd.Flags().GetBool("json")
			if err != nil {
				return adminclient.WithJSONErrorEnvelope(cmd, err, jsonOut)
			}

			if jsonOut {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), string(data))
			} else {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "invite resent to %s\n", email)
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&email, "email", "", "Email address of the pending user (required)")

	return cmd
}
