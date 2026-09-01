package jobs

import (
	"encoding/json/v2"
	"fmt"

	"github.com/djangbahevans/goerp/internal/cli/adminclient"
	"github.com/spf13/cobra"
)

func newRetryCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "retry <job-id>",
		Short: "Retry a retryable or discarded job immediately",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := adminclient.NewFromFlags(cmd)
			if err != nil {
				return err
			}

			data, err := client.Post(cmd.Context(), "/admin/jobs/"+args[0]+"/retry", nil)

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
				return fmt.Errorf("decode job retry response: %w", err)
			}

			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "retried %s (state: %s)\n", view.ID, view.State)
			return nil
		},
	}
}
