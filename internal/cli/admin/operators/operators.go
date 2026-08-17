package operators

import "github.com/spf13/cobra"

func NewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "operators",
		Short: "Manage operator mTLS certificates for the admin gateway",
	}

	cmd.AddCommand(newIssueCertCmd())
	cmd.AddCommand(newRevokeCertCmd())

	return cmd
}
