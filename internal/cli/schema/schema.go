package schema

import "github.com/spf13/cobra"

func NewCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "schema",
		Short: "Manage tenant schema sync (not yet implemented)",
	}
}
