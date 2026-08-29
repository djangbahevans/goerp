package schema

import "github.com/spf13/cobra"

func NewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "schema",
		Short: "Inspect and drive tenant schema sync",
	}

	cmd.AddCommand(newStatusCmd())
	cmd.AddCommand(newDiffCmd())
	cmd.AddCommand(newSyncCmd())
	cmd.AddCommand(newAcceptCmd())

	return cmd
}
