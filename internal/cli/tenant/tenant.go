package tenant

import "github.com/spf13/cobra"

func NewCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "tenant",
		Short: "Manage tenants (not yet implemented)",
	}
}
