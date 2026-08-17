package operators

import (
	"fmt"

	"github.com/djangbahevans/goerp/internal/cli/adminclient"
	"github.com/djangbahevans/goerp/internal/cli/clierr"
	"github.com/spf13/cobra"
)

func newRevokeCertCmd() *cobra.Command {
	var confirm string

	cmd := &cobra.Command{
		Use:   "revoke-cert <name>",
		Short: "Revoke one operator's certificate, blocking that identity at the admin gateway",
		Args:  clierr.WrapArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]

			if confirm != name {
				return clierr.Usage(fmt.Errorf("--confirm must exactly match %q", name))
			}

			client, err := adminclient.NewFromFlags(cmd)
			if err != nil {
				return err
			}

			data, err := client.Post(cmd.Context(), "/admin/operators/revoke-cert", map[string]string{
				"name": name,
			})

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
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "certificate revoked for %q\n", name)
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&confirm, "confirm", "", "Must pass the operator/system name as confirmation (required)")

	return cmd
}
