package jobs

import "github.com/spf13/cobra"

func NewCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "jobs",
		Short: "Administer background jobs (not yet implemented)",
	}
}
