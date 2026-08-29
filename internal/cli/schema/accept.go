package schema

import (
	"encoding/json"
	"fmt"

	"github.com/djangbahevans/goerp/internal/cli/adminclient"
	"github.com/djangbahevans/goerp/internal/cli/clierr"
	"github.com/spf13/cobra"
)

// schemaAcceptResponse mirrors POST /admin/schema/accept's response body
// (internal/engine/adminapi/schema.go's accept handler).
type schemaAcceptResponse struct {
	JobID         string   `json:"job_id"`
	AcceptanceID  string   `json:"acceptance_id,omitempty"`
	AcceptanceIDs []string `json:"acceptance_ids,omitempty"`
}

func newAcceptCmd() *cobra.Command {
	var tenantSlug, reason, confirm string
	var dryRun bool

	cmd := &cobra.Command{
		Use:   "accept <module>",
		Short: "Mark a schema as manually accepted after reconciliation of a blocked migration",
		Args:  clierr.WrapArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			module := args[0]
			if tenantSlug == "" {
				return clierr.Usage(fmt.Errorf("--tenant is required"))
			}

			client, err := adminclient.NewFromFlags(cmd)
			if err != nil {
				return err
			}

			if dryRun {
				return runAcceptDryRun(cmd, client, module, tenantSlug)
			}

			if reason == "" {
				return clierr.Usage(fmt.Errorf("--reason is required"))
			}
			if confirm != module {
				return clierr.Usage(fmt.Errorf("--confirm must exactly match %q", module))
			}

			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "accepting blocked schema change for module %q, tenant %q...\n", module, tenantSlug)

			data, err := client.Post(cmd.Context(), "/admin/schema/accept", map[string]any{
				"module": module,
				"tenant": tenantSlug,
				"reason": reason,
			})

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

			var resp schemaAcceptResponse
			if err := json.Unmarshal(data, &resp); err != nil {
				return fmt.Errorf("decode schema accept response: %w", err)
			}
			printSchemaAcceptResult(cmd, resp)

			return nil
		},
	}

	cmd.Flags().StringVar(&tenantSlug, "tenant", "", "Tenant slug (required)")
	cmd.Flags().StringVar(&reason, "reason", "", "Reason for manual acceptance (logged to audit trail, required)")
	cmd.Flags().StringVar(&confirm, "confirm", "", "Must pass the module name as confirmation")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Show the blocked DDL that would be applied, without accepting or applying it")

	return cmd
}

func printSchemaAcceptResult(cmd *cobra.Command, resp schemaAcceptResponse) {
	out := cmd.OutOrStdout()
	_, _ = fmt.Fprintf(out, "accepted %d blocked change(s): %v\n", len(resp.AcceptanceIDs), resp.AcceptanceIDs)
	_, _ = fmt.Fprintf(out, "resync job started (job_id=%s)\n", resp.JobID)
	_, _ = fmt.Fprintf(out, "check progress with: goerp jobs show %s\n", resp.JobID)
}

// runAcceptDryRun never calls the mutating accept endpoint — it re-runs
// the same live diff `schema diff` would (GET /admin/modules/{name}/schema),
// then shows only the Blocked bucket, since that's the set `accept` would
// authorize (cli-reference.md §4: "the same output `schema diff` would
// show for this module/tenant").
func runAcceptDryRun(cmd *cobra.Command, client *adminclient.Client, module, tenantSlug string) error {
	// verbose=true: non-verbose Diff strips ChangeSummary.Detail down to
	// just kind/table (schema.stripDetail), which is the only field
	// carrying the actual DDL — dry-run's whole purpose is showing that.
	data, err := fetchDiff(cmd.Context(), client, module, tenantSlug, true)

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

	var resp schemaDiffResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return fmt.Errorf("decode schema diff response: %w", err)
	}

	out := cmd.OutOrStdout()
	_, _ = fmt.Fprintf(out, "Module:  %s\n", module)
	_, _ = fmt.Fprintf(out, "Tenant:  %s\n", tenantSlug)
	_, _ = fmt.Fprintf(out, "Version: %s\n", resp.Version)
	if len(resp.Blocked) == 0 {
		_, _ = fmt.Fprintln(out, "\nnothing currently blocked for this module/tenant")
		return nil
	}
	printChangeSet(out, "Blocked", resp.Blocked)

	return nil
}
