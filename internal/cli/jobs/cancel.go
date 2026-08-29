package jobs

import (
	"encoding/json"
	"fmt"

	"github.com/djangbahevans/goerp/internal/cli/adminclient"
	"github.com/spf13/cobra"
)

func newCancelCmd() *cobra.Command {
	var reason string

	cmd := &cobra.Command{
		Use:   "cancel <job-id>",
		Short: "Cancel an available, scheduled, or running job",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := adminclient.NewFromFlags(cmd)
			if err != nil {
				return err
			}

			data, err := client.Post(cmd.Context(), "/admin/jobs/"+args[0]+"/cancel", map[string]string{
				"reason": reason,
			})

			jsonOut, _ := cmd.Flags().GetBool("json")
			if err != nil {
				return adminclient.WithJSONErrorEnvelope(cmd, err, jsonOut)
			}

			if jsonOut {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), string(data))
				return nil
			}

			var view jobView
			if err := json.Unmarshal(data, &view); err != nil {
				return fmt.Errorf("decode job cancel response: %w", err)
			}

			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "cancel requested for %s (state: %s) — cooperative, not preemptive; check `jobs show` for the outcome\n", view.ID, view.State)
			return nil
		},
	}

	cmd.Flags().StringVar(&reason, "reason", "", "Cancellation reason (logged)")

	return cmd
}
