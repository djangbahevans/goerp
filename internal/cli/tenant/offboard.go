package tenant

import (
	"encoding/json/v2"
	"fmt"
	"regexp"
	"strconv"
	"time"

	"github.com/djangbahevans/goerp/internal/cli/adminclient"
	"github.com/djangbahevans/goerp/internal/cli/clierr"
	"github.com/spf13/cobra"
)

// dayDurationPattern mirrors internal/engine/adminapi's own
// parseDayDuration — the CLI validates and previews client-side (Usage
// errors before any network call, and --dry-run's computed schedule),
// but the wire value sent to the server is always the raw --grace-period
// string; the server is the actual authority on what it accepts.
var dayDurationPattern = regexp.MustCompile(`^(\d+)d$`)

// parseGracePeriod accepts a Go duration string ("720h") or a bare day
// count ("30d") — cli-reference.md's `--grace-period` default is "30d", a
// unit Go's own time.ParseDuration doesn't support.
func parseGracePeriod(s string) (time.Duration, error) {
	if m := dayDurationPattern.FindStringSubmatch(s); m != nil {
		days, err := strconv.Atoi(m[1])
		if err != nil {
			return 0, fmt.Errorf("invalid day count %q: %w", s, err)
		}
		return time.Duration(days) * 24 * time.Hour, nil
	}
	return time.ParseDuration(s)
}

// offboardResponse mirrors adminapi.OffboardResult's JSON shape (internal/
// engine/adminapi/tenant.go) — the grace-period path returns status/
// delete_at, the immediate path returns status/job_id.
type offboardResponse struct {
	Status   string     `json:"status"`
	DeleteAt *time.Time `json:"delete_at,omitempty"`
	JobID    string     `json:"job_id,omitempty"`
}

func newOffboardCmd() *cobra.Command {
	var gracePeriod string
	var immediate bool
	var confirm string
	var dryRun bool

	cmd := &cobra.Command{
		Use:   "offboard <slug>",
		Short: "Begin tenant offboarding — schedules deterministic deletion after a grace period, or immediately with --immediate",
		Args:  clierr.WrapArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			slug := args[0]

			parsedGracePeriod, err := parseGracePeriod(gracePeriod)
			if err != nil {
				return clierr.Usage(fmt.Errorf("--grace-period %q is invalid: %w", gracePeriod, err))
			}

			client, err := adminclient.NewFromFlags(cmd)
			if err != nil {
				return err
			}

			if dryRun {
				return runOffboardDryRun(cmd, client, slug, parsedGracePeriod, immediate)
			}

			if confirm != slug {
				return clierr.Usage(fmt.Errorf("--confirm must exactly match %q", slug))
			}

			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "offboarding tenant %q...\n", slug)

			data, err := client.Post(cmd.Context(), fmt.Sprintf("/admin/tenants/%s/offboard", slug), map[string]any{
				"grace_period": gracePeriod,
				"immediate":    immediate,
				"confirm":      confirm,
			})

			jsonOut, _ := cmd.Flags().GetBool("json")
			if err != nil {
				return adminclient.WithJSONErrorEnvelope(cmd, err, jsonOut)
			}

			if jsonOut {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), string(data))
				return nil
			}

			var resp offboardResponse
			if err := json.Unmarshal(data, &resp); err != nil {
				return fmt.Errorf("decode offboard response: %w", err)
			}
			printOffboardResult(cmd, slug, resp)

			return nil
		},
	}

	cmd.Flags().StringVar(&gracePeriod, "grace-period", "30d", "Grace period before data deletion")
	cmd.Flags().BoolVar(&immediate, "immediate", false, "Skip the grace period and delete immediately (irreversible)")
	cmd.Flags().StringVar(&confirm, "confirm", "", "Must pass the tenant slug as confirmation (required unless --dry-run)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Show what would be deleted and when, without scheduling or deleting anything")

	cmd.AddCommand(newOffboardCancelCmd())

	return cmd
}

func printOffboardResult(cmd *cobra.Command, slug string, resp offboardResponse) {
	out := cmd.OutOrStdout()
	if resp.JobID != "" {
		_, _ = fmt.Fprintf(out, "immediate offboard job started for tenant %q (job_id=%s)\n", slug, resp.JobID)
		_, _ = fmt.Fprintf(out, "check progress with: goerp jobs show %s\n", resp.JobID)
		return
	}
	deleteAt := "unknown"
	if resp.DeleteAt != nil {
		deleteAt = resp.DeleteAt.Format(time.RFC3339)
	}
	_, _ = fmt.Fprintf(out, "tenant %q scheduled for deletion at %s\n", slug, deleteAt)
	_, _ = fmt.Fprintf(out, "cancel any time before then with: goerp tenant offboard cancel %s\n", slug)
}

// runOffboardDryRun never calls the mutating offboard endpoint — the
// engine has no server-side dry-run support to call into. Confirming the
// tenant exists via the existing read-only GET /admin/tenants/{slug} (the
// same request `tenant status` makes) is the only network call, and the
// rest of the preview is computed client-side.
func runOffboardDryRun(cmd *cobra.Command, client *adminclient.Client, slug string, gracePeriod time.Duration, immediate bool) error {
	if _, err := client.Get(cmd.Context(), fmt.Sprintf("/admin/tenants/%s", slug)); err != nil {
		return err
	}

	out := cmd.OutOrStdout()
	if immediate {
		_, _ = fmt.Fprintf(out, "would offboard tenant %q immediately (irreversible)\n", slug)
	} else {
		deleteAt := time.Now().Add(gracePeriod).Format(time.RFC3339)
		_, _ = fmt.Fprintf(out, "would offboard tenant %q, scheduling deletion for %s\n", slug, deleteAt)
	}
	_, _ = fmt.Fprintln(out, "would delete: Postgres tenant schema, Redis cache entries, object storage files, Meilisearch indexes")
	_, _ = fmt.Fprintln(out, "would not touch: admin_audit_log, existing backups")

	return nil
}
