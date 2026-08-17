package jobs

import (
	"encoding/json"
	"fmt"

	"github.com/djangbahevans/goerp/internal/cli/adminclient"
	"github.com/spf13/cobra"
)

// jobDetailView mirrors GET /admin/jobs/{id}'s response body
// (internal/engine/adminapi/jobs.go's jobDetailView). Errors is River's
// own per-attempt error/trace history — the basic, no-new-infrastructure
// form of "job logs" this command has, not a redacted structured log.
type jobDetailView struct {
	jobView
	Errors []jobAttemptError `json:"errors"`
}

type jobAttemptError struct {
	At      string `json:"at"`
	Attempt int    `json:"attempt"`
	Error   string `json:"error"`
	Trace   string `json:"trace"`
}

func newShowCmd() *cobra.Command {
	var showLogs bool

	cmd := &cobra.Command{
		Use:   "show <job-id>",
		Short: "Show details for a job",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := adminclient.NewFromFlags(cmd)
			if err != nil {
				return err
			}

			data, err := client.Get(cmd.Context(), "/admin/jobs/"+args[0])

			jsonOut, _ := cmd.Flags().GetBool("json")
			if err != nil {
				if jsonOut {
					if envJSON, ok := adminclient.ErrorEnvelopeJSON(err); ok {
						_, _ = fmt.Fprintln(cmd.OutOrStdout(), string(envJSON))
					}
				}
				return err
			}

			if jsonOut {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), string(data))
				return nil
			}

			var view jobDetailView
			if err := json.Unmarshal(data, &view); err != nil {
				return fmt.Errorf("decode job detail response: %w", err)
			}

			out := cmd.OutOrStdout()
			_, _ = fmt.Fprintf(out, "ID:          %s\n", view.ID)
			_, _ = fmt.Fprintf(out, "Kind:        %s\n", view.Kind)
			_, _ = fmt.Fprintf(out, "Queue:       %s\n", view.Queue)
			_, _ = fmt.Fprintf(out, "State:       %s\n", view.State)
			_, _ = fmt.Fprintf(out, "Attempt:     %d/%d\n", view.Attempt, view.MaxAttempts)
			_, _ = fmt.Fprintf(out, "Priority:    %d\n", view.Priority)
			_, _ = fmt.Fprintf(out, "Created:     %s\n", view.CreatedAt)
			_, _ = fmt.Fprintf(out, "Scheduled:   %s\n", view.ScheduledAt)
			if len(view.Tags) > 0 {
				_, _ = fmt.Fprintf(out, "Tags:        %v\n", view.Tags)
			}

			if showLogs {
				if len(view.Errors) == 0 {
					_, _ = fmt.Fprintln(out, "\nNo attempt errors recorded.")
					return nil
				}
				_, _ = fmt.Fprintln(out, "\nAttempt errors:")
				for _, e := range view.Errors {
					_, _ = fmt.Fprintf(out, "  [%d] %s: %s\n", e.Attempt, e.At, e.Error)
					if e.Trace != "" {
						_, _ = fmt.Fprintf(out, "%s\n", e.Trace)
					}
				}
			}

			return nil
		},
	}

	cmd.Flags().BoolVar(&showLogs, "logs", false, "Show attempt error/trace history")

	return cmd
}
