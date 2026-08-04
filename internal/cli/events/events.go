package events

import "github.com/spf13/cobra"

func NewCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "events",
		Short: "Replay and inspect domain events (not yet implemented)",
	}
}
