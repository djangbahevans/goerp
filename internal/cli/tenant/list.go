package tenant

import (
	"encoding/json/v2"
	"fmt"
	"net/url"
	"strconv"
	"text/tabwriter"
	"time"

	"github.com/djangbahevans/goerp/internal/cli/adminclient"
	"github.com/spf13/cobra"
)

// tenantListResponse mirrors GET /admin/tenants's response body
// (internal/engine/adminapi/tenant.go's list handler) — NextCursor is
// omitted once there's no further page.
type tenantListResponse struct {
	Tenants    []tenantListItem `json:"tenants"`
	NextCursor string           `json:"next_cursor,omitempty"`
}

type tenantListItem struct {
	Slug      string  `json:"slug"`
	Name      string  `json:"name"`
	Plan      string  `json:"plan"`
	Status    string  `json:"status"`
	Country   *string `json:"country,omitempty"`
	CreatedAt string  `json:"created_at"`
	Users     int     `json:"users"`
}

func newListCmd() *cobra.Command {
	var filter, plan, cursor string
	var limit int

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List tenants",
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := adminclient.NewFromFlags(cmd)
			if err != nil {
				return err
			}

			query := url.Values{}
			if filter != "" {
				query.Set("filter", filter)
			}
			if plan != "" {
				query.Set("plan", plan)
			}
			if cursor != "" {
				query.Set("cursor", cursor)
			}
			if limit > 0 {
				query.Set("limit", strconv.Itoa(limit))
			}

			path := "/admin/tenants"
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

			var resp tenantListResponse
			if err := json.Unmarshal(data, &resp); err != nil {
				return fmt.Errorf("decode tenant list response: %w", err)
			}

			printTenantTable(cmd, resp.Tenants)

			if resp.NextCursor != "" {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "more results: --cursor %s\n", resp.NextCursor)
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&filter, "filter", "", "Filter by status: active | provisioning | suspended | offboarded")
	cmd.Flags().StringVar(&plan, "plan", "", "Filter by plan")
	cmd.Flags().StringVar(&cursor, "cursor", "", "Resume from a previous page's next_cursor value")
	cmd.Flags().IntVar(&limit, "limit", 0, "Maximum tenants to return per page (server default if unset)")

	return cmd
}

// printTenantTable renders tenants as a tab-aligned table matching
// cli-reference.md §5's documented `tenant list` columns. CreatedAt is
// reformatted to a bare date for a narrower table; malformed timestamps
// (shouldn't happen against a real admin API) print as-is rather than
// dropping the column.
func printTenantTable(cmd *cobra.Command, tenants []tenantListItem) {
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
	defer func() { _ = w.Flush() }()

	_, _ = fmt.Fprintln(w, "SLUG\tNAME\tPLAN\tSTATUS\tCOUNTRY\tCREATED\tUSERS")
	for _, t := range tenants {
		country := ""
		if t.Country != nil {
			country = *t.Country
		}
		created := t.CreatedAt
		if parsed, err := time.Parse(time.RFC3339, t.CreatedAt); err == nil {
			created = parsed.Format("2006-01-02")
		}
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%d\n", t.Slug, t.Name, t.Plan, t.Status, country, created, t.Users)
	}
}
