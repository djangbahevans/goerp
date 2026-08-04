package module

import "github.com/spf13/cobra"

func NewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "module",
		Short: "Manage GoERP modules",
	}

	cmd.AddCommand(newCreateCmd())

	return cmd
}
