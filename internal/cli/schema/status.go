package schema

import (
	"encoding/json/v2"
	"fmt"
	"net/url"
	"text/tabwriter"
	"time"

	"github.com/djangbahevans/goerp/internal/cli/adminclient"
	"github.com/spf13/cobra"
)

// tenantModuleStatus mirrors schema.TenantModuleStatus's JSON shape —
// GET /admin/schema/status's response body (internal/engine/adminapi/
// schema.go's status handler).
type tenantModuleStatus struct {
	TenantSlug           string     `json:"tenant"`
	ModuleName           string     `json:"module_name"`
	CurrentVersion       string     `json:"current_version"`
	Status               string     `json:"status"`
	SyncedAt             *time.Time `json:"synced_at,omitempty"`
	DataMigrationVersion string     `json:"data_migration_version,omitempty"`
	DataMigrationStatus  string     `json:"data_migration_status,omitempty"`
}

func newStatusCmd() *cobra.Command {
	var tenantSlug, module, filter string

	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show schema sync status for all modules across all tenants",
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := adminclient.NewFromFlags(cmd)
			if err != nil {
				return err
			}

			query := url.Values{}
			if tenantSlug != "" && tenantSlug != "all" {
				query.Set("tenant", tenantSlug)
			}
			if module != "" {
				query.Set("module", module)
			}
			if filter != "" {
				query.Set("filter", filter)
			}

			path := "/admin/schema/status"
			if len(query) > 0 {
				path += "?" + query.Encode()
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

			var statuses []tenantModuleStatus
			if err := json.Unmarshal(data, &statuses); err != nil {
				return fmt.Errorf("decode schema status response: %w", err)
			}

			printSchemaStatus(cmd, statuses)

			return nil
		},
	}

	cmd.Flags().StringVar(&tenantSlug, "tenant", "all", "Filter to a specific tenant slug, or \"all\"")
	cmd.Flags().StringVar(&module, "module", "", "Filter to a specific module")
	cmd.Flags().StringVar(&filter, "filter", "", "Filter by status: ok | failed | in_progress | pending")

	return cmd
}

func printSchemaStatus(cmd *cobra.Command, statuses []tenantModuleStatus) {
	out := cmd.OutOrStdout()
	if len(statuses) == 0 {
		_, _ = fmt.Fprintln(out, "no schema sync records found")
		return
	}

	w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "MODULE\tVERSION\tTENANT\tSCHEMA STATUS\tDATA MIGRATION\tLAST SYNCED")
	for _, s := range statuses {
		syncedAt := "-"
		if s.SyncedAt != nil {
			syncedAt = s.SyncedAt.Format("2006-01-02 15:04:05")
		}
		dataMigration := s.DataMigrationStatus
		if dataMigration == "" {
			dataMigration = "-"
		}
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
			s.ModuleName, s.CurrentVersion, s.TenantSlug, s.Status, dataMigration, syncedAt)
	}
	_ = w.Flush()
}
