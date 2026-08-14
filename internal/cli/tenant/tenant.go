package tenant

import "github.com/spf13/cobra"

func NewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "tenant",
		Short: "Manage tenants",
	}

	cmd.AddCommand(newResendInviteCmd())

	return cmd
}
