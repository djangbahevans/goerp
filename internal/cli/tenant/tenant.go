package tenant

import "github.com/spf13/cobra"

func NewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "tenant",
		Short: "Manage tenants",
	}

	cmd.AddCommand(newStatusCmd())
	cmd.AddCommand(newListCmd())
	cmd.AddCommand(newResendInviteCmd())
	cmd.AddCommand(newSuspendCmd())
	cmd.AddCommand(newUnsuspendCmd())
	cmd.AddCommand(newOffboardCmd())

	return cmd
}
