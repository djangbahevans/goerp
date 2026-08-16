package tenant

import (
	"encoding/json"
	"fmt"
	"text/tabwriter"
	"time"

	"github.com/djangbahevans/goerp/internal/cli/adminclient"
	"github.com/djangbahevans/goerp/internal/cli/clierr"
	"github.com/spf13/cobra"
)

// tenantStatusResponse mirrors GET /admin/tenants/{slug}'s response body
// (internal/engine/adminapi/tenant.go's status handler, tenantStatusResponse).
type tenantStatusResponse struct {
	Slug      string  `json:"slug"`
	Name      string  `json:"name"`
	Plan      string  `json:"plan"`
	Status    string  `json:"status"`
	Region    string  `json:"region"`
	Country   *string `json:"country,omitempty"`
	CreatedAt string  `json:"created_at"`

	SchemaTableCount     int                     `json:"schema_table_count,omitempty"`
	ModulesSynced        int                     `json:"modules_synced"`
	ModulesTotal         int                     `json:"modules_total"`
	Modules              []moduleSyncStatusEntry `json:"modules,omitempty"`
	ProvisioningDuration string                  `json:"provisioning_duration,omitempty"`
	AdminUser            *adminUserInfo          `json:"admin_user,omitempty"`
}

// moduleSyncStatusEntry mirrors schema.ModuleSyncStatus's JSON shape.
type moduleSyncStatusEntry struct {
	ModuleName     string     `json:"module_name"`
	CurrentVersion string     `json:"current_version"`
	Status         string     `json:"status"`
	SyncedAt       *time.Time `json:"synced_at,omitempty"`
}

// adminUserInfo mirrors adminapi.AdminUserInfo — id/email only, since
// system.users has no display-name column.
type adminUserInfo struct {
	ID    string `json:"id"`
	Email string `json:"email"`
}

func newStatusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status <slug>",
		Short: "Show detailed status for a single tenant, including schema sync state for each module",
		Args:  clierr.WrapArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			slug := args[0]

			client, err := adminclient.NewFromFlags(cmd)
			if err != nil {
				return err
			}

			data, err := client.Get(cmd.Context(), fmt.Sprintf("/admin/tenants/%s", slug))

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

			var resp tenantStatusResponse
			if err := json.Unmarshal(data, &resp); err != nil {
				return fmt.Errorf("decode tenant status response: %w", err)
			}

			printTenantStatus(cmd, resp)

			return nil
		},
	}

	return cmd
}

// printTenantStatus renders a label/value block for the tenant's own
// fields plus the sync ratio/schema table count/admin user/provisioning
// duration, followed by a per-module sync table — cli-reference.md §5
// "goerp tenant status" documents this as the detailed single-tenant view,
// unlike `tenant list`'s columnar table.
func printTenantStatus(cmd *cobra.Command, resp tenantStatusResponse) {
	out := cmd.OutOrStdout()

	country := ""
	if resp.Country != nil {
		country = *resp.Country
	}
	created := resp.CreatedAt
	if parsed, err := time.Parse(time.RFC3339, resp.CreatedAt); err == nil {
		created = parsed.Format("2006-01-02")
	}

	_, _ = fmt.Fprintf(out, "Slug:                  %s\n", resp.Slug)
	_, _ = fmt.Fprintf(out, "Name:                  %s\n", resp.Name)
	_, _ = fmt.Fprintf(out, "Plan:                  %s\n", resp.Plan)
	_, _ = fmt.Fprintf(out, "Status:                %s\n", resp.Status)
	_, _ = fmt.Fprintf(out, "Region:                %s\n", resp.Region)
	_, _ = fmt.Fprintf(out, "Country:               %s\n", country)
	_, _ = fmt.Fprintf(out, "Created:               %s\n", created)
	_, _ = fmt.Fprintf(out, "Schema table count:    %d\n", resp.SchemaTableCount)
	_, _ = fmt.Fprintf(out, "Modules synced:        %d/%d\n", resp.ModulesSynced, resp.ModulesTotal)
	if resp.ProvisioningDuration != "" {
		_, _ = fmt.Fprintf(out, "Provisioning duration: %s\n", resp.ProvisioningDuration)
	}
	if resp.AdminUser != nil {
		_, _ = fmt.Fprintf(out, "Admin user:            %s (%s)\n", resp.AdminUser.Email, resp.AdminUser.ID)
	}

	if len(resp.Modules) == 0 {
		return
	}

	_, _ = fmt.Fprintln(out)
	w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "MODULE\tVERSION\tSTATUS\tSYNCED AT")
	for _, m := range resp.Modules {
		syncedAt := "-"
		if m.SyncedAt != nil {
			syncedAt = m.SyncedAt.Format("2006-01-02 15:04:05")
		}
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", m.ModuleName, m.CurrentVersion, m.Status, syncedAt)
	}
	_ = w.Flush()
}
