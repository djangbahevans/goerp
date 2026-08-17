package jobs

import "github.com/spf13/cobra"

func NewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "jobs",
		Short: "Administer background jobs",
	}

	cmd.AddCommand(newListCmd())
	cmd.AddCommand(newShowCmd())
	cmd.AddCommand(newRetryCmd())
	cmd.AddCommand(newCancelCmd())

	return cmd
}
