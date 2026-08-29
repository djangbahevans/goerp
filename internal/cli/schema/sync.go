package schema

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/djangbahevans/goerp/internal/cli/adminclient"
	"github.com/djangbahevans/goerp/internal/cli/clierr"
	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"
)

// pollInterval mirrors internal/cli/tenant's own constant — how often
// --wait polls GET /admin/jobs/{id} while a sync job runs.
const pollInterval = 2 * time.Second

// parseSchedule mirrors internal/engine/adminapi's own — cli-reference.md
// §4: "a bare, zone-less timestamp is rejected, not interpreted as local
// time." Validating here gives a Usage error before any network call, the
// same "validate client-side, but the wire value sent is the raw string"
// split offboard.go's --grace-period already uses; the server remains the
// actual authority on what it accepts.
func parseSchedule(s string) error {
	if s == "" {
		return nil
	}
	if _, err := time.Parse(time.RFC3339, s); err != nil {
		return fmt.Errorf("--schedule must be RFC 3339 with an explicit offset or \"Z\" (e.g. \"2026-05-09T02:00:00Z\"): %w", err)
	}
	return nil
}

type schemaSyncStartResponse struct {
	JobID string `json:"job_id"`
}

type schemaSyncJobDetail struct {
	State  string          `json:"state"`
	Output json.RawMessage `json:"output,omitempty"`
}

// schemaSyncResult mirrors tenantsync.SyncResult's JSON shape.
type schemaSyncResult struct {
	Synced []schemaSyncPairResult `json:"synced"`
	Failed []schemaSyncPairResult `json:"failed"`
}

type schemaSyncPairResult struct {
	Tenant string `json:"tenant"`
	Module string `json:"module"`
	Error  string `json:"error,omitempty"`
}

func newSyncCmd() *cobra.Command {
	var tenantSlug, module, schedule string
	var timeout time.Duration
	var wait bool

	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Manually trigger schema sync for one or all tenants",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := parseSchedule(schedule); err != nil {
				return clierr.Usage(err)
			}

			jsonOut, _ := cmd.Flags().GetBool("json")
			yes, _ := cmd.Flags().GetBool("yes")

			client, err := adminclient.NewFromFlags(cmd)
			if err != nil {
				return err
			}

			if tenantSlug == "" && module == "" && !yes {
				// cli-reference.md §2b: "--json implies noninteractive... a
				// Tier 2 command run with --json but missing its
				// confirmation flag fails... rather than falling back to a
				// prompt," and separately, any broad-target prompt "fails
				// immediately... whenever stdin isn't a TTY — it never
				// blocks waiting for input." Both cases require --yes
				// up front rather than attempting (and either silently
				// skipping, per --json, or hanging on, per a non-TTY
				// stdin) the interactive read below.
				if jsonOut || !isInteractiveStdin(cmd) {
					return clierr.Usage(fmt.Errorf("schema sync targeting all tenants and all modules requires --yes when running non-interactively (stdin is not a terminal, or --json is set)"))
				}

				confirmed, err := confirmBroadSync(cmd, client)
				if err != nil {
					return err
				}
				if !confirmed {
					return fmt.Errorf("aborted: schema sync targeting all tenants and all modules was not confirmed")
				}
			}

			_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "starting schema sync...")

			data, err := client.Post(cmd.Context(), "/admin/schema/sync", map[string]any{
				"tenant":   tenantSlug,
				"module":   module,
				"schedule": schedule,
			})
			if err != nil {
				if jsonOut {
					if envJSON, ok := adminclient.ErrorEnvelopeJSON(err); ok {
						_, _ = fmt.Fprintln(cmd.OutOrStdout(), string(envJSON))
					}
				}
				return err
			}

			var started schemaSyncStartResponse
			if err := json.Unmarshal(data, &started); err != nil {
				return fmt.Errorf("decode schema sync response: %w", err)
			}

			// A scheduled sync returns its job_id immediately and is never
			// waited on — cli-reference.md §4: "the CLI does not stay
			// running to 'hold' the schedule." --wait only applies to an
			// immediate sync.
			if !wait || schedule != "" {
				if jsonOut {
					_, _ = fmt.Fprintln(cmd.OutOrStdout(), string(data))
					return nil
				}
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "schema sync job started (job_id=%s)\n", started.JobID)
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "check progress with: goerp jobs show %s\n", started.JobID)
				return nil
			}

			result, err := waitForSchemaSync(cmd, client, started.JobID, timeout)
			if err != nil {
				return err
			}

			if jsonOut {
				out, err := json.Marshal(result)
				if err != nil {
					return fmt.Errorf("encode schema sync result: %w", err)
				}
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), string(out))
				return nil
			}

			printSchemaSyncResult(cmd, result)

			return nil
		},
	}

	cmd.Flags().StringVar(&tenantSlug, "tenant", "", "Tenant slug to sync, or omit for all tenants")
	cmd.Flags().StringVar(&module, "module", "", "Module to sync (default: all modules)")
	cmd.Flags().BoolVar(&wait, "wait", true, "Block until sync completes")
	// Local "timeout" deliberately shadows the root persistent flag of the
	// same name (adminclient.NewFromFlags picks up whichever is nearest) —
	// cli-reference.md §4 documents this command's own default as 10m,
	// distinct from §2's global 30s, and §2b's exit code 124 is defined as
	// "--timeout elapsed while polling an async job," so the one value
	// governs both this command's own admin API request timeout and how
	// long --wait polls before giving up.
	cmd.Flags().DurationVar(&timeout, "timeout", 10*time.Minute, "Sync timeout")
	cmd.Flags().StringVar(&schedule, "schedule", "", "Run sync at a specific time — RFC 3339 with an explicit offset or \"Z\" required")

	return cmd
}

// isInteractiveStdin reports whether cmd's stdin is a real terminal —
// cli-reference.md §2b's noninteractive-mode rule turns on this, not on
// whether a TTY is merely absent from some flag; a script piping a fixed
// "y\n" into stdin is exactly the case that rule exists to catch.
func isInteractiveStdin(cmd *cobra.Command) bool {
	f, ok := cmd.InOrStdin().(*os.File)
	return ok && isatty.IsTerminal(f.Fd())
}

// confirmBroadSync previews the affected tenant/module count for an
// omit-both-flags sync and reads an interactive y/N answer — the first
// interactive prompt in this CLI (cli-reference.md §4's broad-target
// rule). Callers only reach this once stdin is already known to be a real
// TTY (isInteractiveStdin) and --json is off. There is no admin API route
// enumerating loaded modules (goerp#30's scope is the four existing
// schema routes only), so the preview counts distinct tenants/modules
// from the unfiltered GET /admin/schema/status listing — an
// approximation of what the sync job will actually resolve
// (SyncWorker.resolveTenants/resolveModules), good enough for a human
// sanity check before a broad, mutating call.
func confirmBroadSync(cmd *cobra.Command, client *adminclient.Client) (bool, error) {
	data, err := client.Get(cmd.Context(), "/admin/schema/status")
	if err != nil {
		return false, err
	}

	var statuses []tenantModuleStatus
	if err := json.Unmarshal(data, &statuses); err != nil {
		return false, fmt.Errorf("decode schema status response: %w", err)
	}

	tenants := map[string]struct{}{}
	modules := map[string]struct{}{}
	for _, s := range statuses {
		tenants[s.TenantSlug] = struct{}{}
		modules[s.ModuleName] = struct{}{}
	}

	_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "%d tenant(s) × %d module(s)\n", len(tenants), len(modules))
	_, _ = fmt.Fprint(cmd.ErrOrStderr(), "Continue? [y/N]: ")

	reader := bufio.NewReader(cmd.InOrStdin())
	line, _ := reader.ReadString('\n')
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes", nil
}

func waitForSchemaSync(cmd *cobra.Command, client *adminclient.Client, jobID string, timeout time.Duration) (schemaSyncResult, error) {
	deadline := time.Now().Add(timeout)
	path := "/admin/jobs/" + jobID

	for {
		data, err := client.Get(cmd.Context(), path)
		if err != nil {
			return schemaSyncResult{}, err
		}

		var detail schemaSyncJobDetail
		if err := json.Unmarshal(data, &detail); err != nil {
			return schemaSyncResult{}, fmt.Errorf("decode job detail response: %w", err)
		}

		switch detail.State {
		case "completed":
			if len(detail.Output) == 0 {
				return schemaSyncResult{}, fmt.Errorf("schema sync job %s completed with no recorded output", jobID)
			}
			var result schemaSyncResult
			if err := json.Unmarshal(detail.Output, &result); err != nil {
				return schemaSyncResult{}, fmt.Errorf("decode schema sync result: %w", err)
			}
			return result, nil
		case "cancelled", "discarded":
			return schemaSyncResult{}, fmt.Errorf("schema sync job %s did not complete (state=%s) — check `goerp jobs show %s --logs`", jobID, detail.State, jobID)
		}

		if time.Now().After(deadline) {
			return schemaSyncResult{}, &clierr.Error{
				Code: 124,
				Err:  fmt.Errorf("schema sync job %s still %q after %s — check `goerp jobs show %s`", jobID, detail.State, timeout, jobID),
			}
		}

		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "schema sync job %s still %s...\n", jobID, detail.State)

		select {
		case <-cmd.Context().Done():
			return schemaSyncResult{}, cmd.Context().Err()
		case <-time.After(pollInterval):
		}
	}
}

func printSchemaSyncResult(cmd *cobra.Command, result schemaSyncResult) {
	out := cmd.OutOrStdout()
	_, _ = fmt.Fprintf(out, "synced: %d, failed: %d\n", len(result.Synced), len(result.Failed))
	for _, f := range result.Failed {
		_, _ = fmt.Fprintf(out, "  FAILED tenant=%s module=%s: %s\n", f.Tenant, f.Module, f.Error)
	}
}
