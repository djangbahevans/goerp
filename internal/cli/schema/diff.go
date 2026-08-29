package schema

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"text/tabwriter"

	"github.com/djangbahevans/goerp/internal/cli/adminclient"
	"github.com/djangbahevans/goerp/internal/cli/clierr"
	"github.com/spf13/cobra"
)

// changeSummary mirrors schema.ChangeSummary's JSON shape.
type changeSummary struct {
	Kind   string `json:"kind"`
	Table  string `json:"table"`
	Detail string `json:"detail,omitempty"`
	Hash   string `json:"hash,omitempty"`
}

// schemaDiffResponse mirrors GET /admin/modules/{name}/schema's response
// body (internal/engine/adminapi/schema.go's diff handler,
// schemaDiffResponse).
type schemaDiffResponse struct {
	Module   string          `json:"module"`
	Tenant   string          `json:"tenant"`
	Version  string          `json:"version"`
	Safe     []changeSummary `json:"safe"`
	Deferred []changeSummary `json:"deferred"`
	Blocked  []changeSummary `json:"blocked"`
}

func newDiffCmd() *cobra.Command {
	var tenantSlug, module string
	var all, verbose bool

	cmd := &cobra.Command{
		Use:   "diff",
		Short: "Show the DDL that would be applied to bring a tenant's schema up to the current module declaration",
		RunE: func(cmd *cobra.Command, args []string) error {
			if module == "" {
				return clierr.Usage(fmt.Errorf("--module is required"))
			}
			if !all && tenantSlug == "" {
				return clierr.Usage(fmt.Errorf("--tenant is required unless --all is set"))
			}
			if all && tenantSlug != "" {
				return clierr.Usage(fmt.Errorf("--tenant and --all are mutually exclusive"))
			}

			client, err := adminclient.NewFromFlags(cmd)
			if err != nil {
				return err
			}

			jsonOut, _ := cmd.Flags().GetBool("json")

			tenants := []string{tenantSlug}
			if all {
				tenants, err = listAllTenantSlugs(cmd.Context(), client)
				if err != nil {
					return adminclient.WithJSONErrorEnvelope(cmd, err, jsonOut)
				}
			}

			var responses []schemaDiffResponse
			var rawResponses []json.RawMessage
			for _, slug := range tenants {
				data, err := fetchDiff(cmd.Context(), client, module, slug, verbose)
				if err != nil {
					return adminclient.WithJSONErrorEnvelope(cmd, err, jsonOut)
				}

				if jsonOut {
					rawResponses = append(rawResponses, data)
					continue
				}

				var resp schemaDiffResponse
				if err := json.Unmarshal(data, &resp); err != nil {
					return fmt.Errorf("decode schema diff response: %w", err)
				}
				responses = append(responses, resp)
			}

			if jsonOut {
				if all {
					out, err := json.Marshal(rawResponses)
					if err != nil {
						return fmt.Errorf("encode schema diff responses: %w", err)
					}
					_, _ = fmt.Fprintln(cmd.OutOrStdout(), string(out))
					return nil
				}
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), string(rawResponses[0]))
				return nil
			}

			for _, resp := range responses {
				printSchemaDiff(cmd, resp)
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&tenantSlug, "tenant", "", "Tenant slug to diff against (required unless --all)")
	cmd.Flags().StringVar(&module, "module", "", "Module to diff (required)")
	cmd.Flags().BoolVar(&all, "all", false, "Diff all tenants for the specified module")
	cmd.Flags().BoolVar(&verbose, "verbose", false, "Include full column definitions (not just change summary)")

	return cmd
}

func fetchDiff(ctx context.Context, client *adminclient.Client, module, tenantSlug string, verbose bool) (json.RawMessage, error) {
	query := url.Values{}
	query.Set("tenant", tenantSlug)
	if verbose {
		query.Set("verbose", "true")
	}
	path := fmt.Sprintf("/admin/modules/%s/schema?%s", url.PathEscape(module), query.Encode())
	return client.Get(ctx, path)
}

// listAllTenantSlugs follows GET /admin/tenants's next_cursor to the end —
// `schema diff --all` needs every tenant, unlike `tenant list`'s own
// single-page-plus-hint behavior, since diffing only some tenants for
// "all tenants" would silently under-report.
func listAllTenantSlugs(ctx context.Context, client *adminclient.Client) ([]string, error) {
	var slugs []string
	cursor := ""
	for {
		query := url.Values{}
		if cursor != "" {
			query.Set("cursor", cursor)
		}
		path := "/admin/tenants"
		if encoded := query.Encode(); encoded != "" {
			path += "?" + encoded
		}

		data, err := client.Get(ctx, path)
		if err != nil {
			return nil, err
		}

		var page struct {
			Tenants []struct {
				Slug string `json:"slug"`
			} `json:"tenants"`
			NextCursor string `json:"next_cursor,omitempty"`
		}
		if err := json.Unmarshal(data, &page); err != nil {
			return nil, fmt.Errorf("decode tenant list response: %w", err)
		}

		for _, t := range page.Tenants {
			slugs = append(slugs, t.Slug)
		}

		if page.NextCursor == "" {
			return slugs, nil
		}
		cursor = page.NextCursor
	}
}

func printSchemaDiff(cmd *cobra.Command, resp schemaDiffResponse) {
	out := cmd.OutOrStdout()
	_, _ = fmt.Fprintf(out, "Module:  %s\n", resp.Module)
	_, _ = fmt.Fprintf(out, "Tenant:  %s\n", resp.Tenant)
	_, _ = fmt.Fprintf(out, "Version: %s\n", resp.Version)

	printChangeSet(out, "Safe", resp.Safe)
	printChangeSet(out, "Deferred", resp.Deferred)
	printChangeSet(out, "Blocked", resp.Blocked)
	_, _ = fmt.Fprintln(out)
}

func printChangeSet(out io.Writer, label string, changes []changeSummary) {
	if len(changes) == 0 {
		return
	}
	_, _ = fmt.Fprintf(out, "\n%s:\n", label)
	w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "KIND\tTABLE\tDETAIL")
	for _, c := range changes {
		detail := c.Detail
		if detail == "" {
			detail = "-"
		}
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\n", c.Kind, c.Table, detail)
	}
	_ = w.Flush()
}
