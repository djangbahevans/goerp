package jobs

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"text/tabwriter"

	"github.com/djangbahevans/goerp/internal/cli/adminclient"
	"github.com/spf13/cobra"
)

// jobView mirrors GET /admin/jobs's response body
// (internal/engine/adminapi/jobs.go's jobView).
type jobView struct {
	ID          string   `json:"id"`
	Kind        string   `json:"kind"`
	Queue       string   `json:"queue"`
	State       string   `json:"state"`
	Attempt     int      `json:"attempt"`
	MaxAttempts int      `json:"max_attempts"`
	Priority    int      `json:"priority"`
	CreatedAt   string   `json:"created_at"`
	ScheduledAt string   `json:"scheduled_at"`
	Tags        []string `json:"tags"`
}

func newListCmd() *cobra.Command {
	var queue, status, jobType, tenant, since string
	var limit int

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List jobs in the queue",
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := adminclient.NewFromFlags(cmd)
			if err != nil {
				return err
			}

			query := url.Values{}
			if queue != "" {
				query.Set("queue", queue)
			}
			if status != "" {
				query.Set("status", status)
			}
			if jobType != "" {
				query.Set("type", jobType)
			}
			if tenant != "" {
				query.Set("tenant", tenant)
			}
			if since != "" {
				query.Set("since", since)
			}
			if limit > 0 {
				query.Set("limit", strconv.Itoa(limit))
			}

			path := "/admin/jobs"
			if encoded := query.Encode(); encoded != "" {
				path += "?" + encoded
			}

			data, err := client.Get(cmd.Context(), path)

			jsonOut, _ := cmd.Flags().GetBool("json")
			if err != nil {
				return adminclient.WithJSONErrorEnvelope(cmd, err, jsonOut)
			}

			if jsonOut {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), string(data))
				return nil
			}

			var views []jobView
			if err := json.Unmarshal(data, &views); err != nil {
				return fmt.Errorf("decode job list response: %w", err)
			}

			printJobTable(cmd, views)
			return nil
		},
	}

	cmd.Flags().StringVar(&queue, "queue", "", "Filter by queue name (default, critical, bulk, search, email)")
	cmd.Flags().StringVar(&status, "status", "", "Filter by status: available | scheduled | running | retryable | completed | cancelled | discarded")
	cmd.Flags().StringVar(&jobType, "type", "", "Filter by job type (kind)")
	cmd.Flags().StringVar(&tenant, "tenant", "", "Filter by tenant — only matches job types that record tenant_id in their own job metadata")
	cmd.Flags().StringVar(&since, "since", "", "Show jobs created since (duration string, e.g. \"1h\", \"30m\"; default: 1h)")
	cmd.Flags().IntVar(&limit, "limit", 50, "Maximum jobs to show")

	return cmd
}

func printJobTable(cmd *cobra.Command, views []jobView) {
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
	defer func() { _ = w.Flush() }()

	_, _ = fmt.Fprintln(w, "ID\tKIND\tQUEUE\tSTATE\tATTEMPT\tCREATED")
	for _, j := range views {
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%d/%d\t%s\n", j.ID, j.Kind, j.Queue, j.State, j.Attempt, j.MaxAttempts, j.CreatedAt)
	}
}
