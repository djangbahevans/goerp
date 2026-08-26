package events

import "github.com/spf13/cobra"

func NewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "events",
		Short: "Replay and inspect domain events",
	}
	cmd.AddCommand(newReplayCmd())
	return cmd
}
