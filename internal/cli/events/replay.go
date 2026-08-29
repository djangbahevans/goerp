package events

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"time"

	"github.com/djangbahevans/goerp/internal/cli/adminclient"
	"github.com/djangbahevans/goerp/internal/cli/clierr"
	"github.com/spf13/cobra"
)

// replayConfirmPhrase mirrors adminapi's own replayConfirmPhrase — the
// literal, non-guessable value cli-reference.md §2a requires for a
// broad-target Tier 2 operation with no single target to name.
const replayConfirmPhrase = "REPLAY EVENTS"

// relativeDurationPattern accepts "Nh" or "Nd" — cli-reference.md §8's
// documented `--from`/`--to` shorthand ("2h", "7d"). Go's own
// time.ParseDuration handles "h"/"m"/"s" but not "d" (days), the same gap
// internal/cli/tenant/offboard.go's dayDurationPattern already works
// around for --grace-period, generalized here to also accept hours.
var relativeDurationPattern = regexp.MustCompile(`^(\d+)([hd])$`)

// parseTimeOrRelative resolves --from/--to to an absolute instant before
// it ever reaches the wire: RFC 3339 is used as-is, "Nh"/"Nd" resolves to
// now-N. The request body the admin API receives always carries an
// absolute RFC 3339 timestamp — a relative string has no fixed meaning
// once it reaches the server later, so resolving it client-side, once,
// against the CLI's own clock is the only sane wire contract.
func parseTimeOrRelative(s string, now time.Time) (time.Time, error) {
	if m := relativeDurationPattern.FindStringSubmatch(s); m != nil {
		n, err := strconv.Atoi(m[1])
		if err != nil {
			return time.Time{}, fmt.Errorf("invalid relative duration %q: %w", s, err)
		}
		unit := time.Hour
		if m[2] == "d" {
			unit = 24 * time.Hour
		}
		return now.Add(-time.Duration(n) * unit), nil
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("must be RFC 3339 or a relative duration like \"2h\"/\"7d\": %w", err)
	}
	return t, nil
}

type replayResponse struct {
	JobID     string `json:"job_id"`
	StatusURL string `json:"status_url"`
}

type replayDryRunResponse struct {
	EventCount               int `json:"event_count"`
	JobCount                 int `json:"job_count"`
	EstimatedDurationMinutes int `json:"estimated_duration_minutes"`
}

func newReplayCmd() *cobra.Command {
	var from, to, module, confirm string
	var events, subscribers []string
	var tenantSlug string
	var dryRun bool
	var batchSize int

	cmd := &cobra.Command{
		Use:   "replay",
		Short: "Replay events from the event log to one or more subscribers",
		RunE: func(cmd *cobra.Command, args []string) error {
			now := time.Now()
			fromTime, err := parseTimeOrRelative(from, now)
			if err != nil {
				return clierr.Usage(fmt.Errorf("--from %q is invalid: %w", from, err))
			}
			var toTime time.Time
			if to != "" {
				toTime, err = parseTimeOrRelative(to, now)
				if err != nil {
					return clierr.Usage(fmt.Errorf("--to %q is invalid: %w", to, err))
				}
			}

			// Broad-target rule (cli-reference.md §2a): omitting
			// --subscriber replays to every current subscriber, and
			// --tenant "all" spans every active tenant — either makes
			// this the "footgun" case Tier 2's confirm-by-value gate
			// exists to catch. Checked client-side too (not just
			// server-side) so a scripted misuse fails before any
			// network call, matching tenant offboard's own pattern.
			if !dryRun && (len(subscribers) == 0 || tenantSlug == "all") && confirm != replayConfirmPhrase {
				return clierr.Usage(fmt.Errorf("--confirm must exactly match %q", replayConfirmPhrase))
			}

			client, err := adminclient.NewFromFlags(cmd)
			if err != nil {
				return err
			}

			body := map[string]any{
				"tenant":     tenantSlug,
				"event":      events,
				"module":     module,
				"subscriber": subscribers,
				"from":       fromTime.Format(time.RFC3339),
				"batch_size": batchSize,
				"dry_run":    dryRun,
				"confirm":    confirm,
			}
			if !toTime.IsZero() {
				body["to"] = toTime.Format(time.RFC3339)
			}

			if !dryRun {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "replaying events for tenant %q...\n", tenantSlug)
			}

			data, err := client.Post(cmd.Context(), "/admin/events/replay", body)

			jsonOut, _ := cmd.Flags().GetBool("json")
			if err != nil {
				return adminclient.WithJSONErrorEnvelope(cmd, err, jsonOut)
			}

			if jsonOut {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), string(data))
				return nil
			}

			out := cmd.OutOrStdout()
			if dryRun {
				var resp replayDryRunResponse
				if err := json.Unmarshal(data, &resp); err != nil {
					return fmt.Errorf("decode dry-run response: %w", err)
				}
				_, _ = fmt.Fprintf(out, "would replay %d matching events to %d subscriber deliveries (est. %d min)\n",
					resp.EventCount, resp.JobCount, resp.EstimatedDurationMinutes)
				return nil
			}

			var resp replayResponse
			if err := json.Unmarshal(data, &resp); err != nil {
				return fmt.Errorf("decode replay response: %w", err)
			}
			_, _ = fmt.Fprintf(out, "replay job started (job_id=%s)\n", resp.JobID)
			_, _ = fmt.Fprintf(out, "check progress with: goerp jobs show %s\n", resp.JobID)
			return nil
		},
	}

	cmd.Flags().StringVar(&from, "from", "", "Replay events from this timestamp (RFC 3339 or relative: \"2h\", \"7d\")")
	cmd.Flags().StringVar(&to, "to", "", "Replay events up to this timestamp (default: now)")
	cmd.Flags().StringArrayVar(&events, "event", nil, "Event name to replay (repeatable)")
	cmd.Flags().StringVar(&module, "module", "", "Filter to events emitted by a specific module")
	cmd.Flags().StringArrayVar(&subscribers, "subscriber", nil, "Target one subscriber module (repeatable). Omitting this replays to every current subscriber")
	cmd.Flags().StringVar(&tenantSlug, "tenant", "", `Tenant slug, or "all" to replay across every active tenant sequentially`)
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Show the matching event count and estimated job count without replaying")
	cmd.Flags().IntVar(&batchSize, "batch-size", 100, "Events per River job batch")
	cmd.Flags().StringVar(&confirm, "confirm", "", `Required whenever --subscriber is omitted or --tenant is "all": must equal "REPLAY EVENTS"`)

	_ = cmd.MarkFlagRequired("from")
	_ = cmd.MarkFlagRequired("event")
	_ = cmd.MarkFlagRequired("tenant")

	return cmd
}
