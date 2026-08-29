package tenant

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/djangbahevans/goerp/internal/cli/adminclient"
	"github.com/djangbahevans/goerp/internal/cli/clierr"
	"github.com/spf13/cobra"
)

// createTenantResponse mirrors POST /admin/tenants's response body
// (internal/engine/adminapi/tenant.go's create handler).
type createTenantResponse struct {
	Slug       string `json:"slug"`
	WorkflowID string `json:"workflow_id"`
}

func newCreateCmd() *cobra.Command {
	var name string
	var adminEmail string
	var adminName string
	var wait bool
	var waitTimeout time.Duration

	cmd := &cobra.Command{
		Use:   "create <slug>",
		Short: "Provision a new tenant and, with --wait (the default), block until it's active",
		Args:  clierr.WrapArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			slug := args[0]

			if adminEmail == "" {
				return clierr.Usage(fmt.Errorf("--admin-email is required"))
			}
			if name == "" {
				name = slug
			}

			client, err := adminclient.NewFromFlags(cmd)
			if err != nil {
				return err
			}

			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "provisioning tenant %q...\n", slug)

			data, err := client.Post(cmd.Context(), "/admin/tenants", map[string]string{
				"slug":        slug,
				"name":        name,
				"admin_email": adminEmail,
				"admin_name":  adminName,
			})

			jsonOut, _ := cmd.Flags().GetBool("json")
			if err != nil {
				return adminclient.WithJSONErrorEnvelope(cmd, err, jsonOut)
			}

			var created createTenantResponse
			if err := json.Unmarshal(data, &created); err != nil {
				return fmt.Errorf("decode create response: %w", err)
			}

			if !wait {
				if jsonOut {
					_, _ = fmt.Fprintln(cmd.OutOrStdout(), string(data))
					return nil
				}
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "tenant %q provisioning started (workflow_id=%s)\n", slug, created.WorkflowID)
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "check progress with: goerp tenant status %s\n", slug)
				return nil
			}

			return waitForActive(cmd, client, slug, waitTimeout, jsonOut)
		},
	}

	cmd.Flags().StringVar(&name, "name", "", "Tenant display name (default: slug)")
	cmd.Flags().StringVar(&adminEmail, "admin-email", "", "Admin user email (required)")
	cmd.Flags().StringVar(&adminName, "admin-name", "", "Admin user full name")
	cmd.Flags().BoolVar(&wait, "wait", true, "Block until provisioning completes")
	cmd.Flags().DurationVar(&waitTimeout, "wait-timeout", 5*time.Minute, "How long --wait waits before giving up")

	return cmd
}

// waitForActive polls GET /admin/tenants/{slug} (the same request `tenant
// status` makes) until tenant.Status reaches "active" or timeout elapses.
// tenant.Status has no "failed" value (internal/engine/tenant/tenant.go) —
// a stuck workflow just leaves the tenant at "provisioning" indefinitely,
// so timeout, not a failure status, is what bounds the wait.
func waitForActive(cmd *cobra.Command, client *adminclient.Client, slug string, timeout time.Duration, jsonOut bool) error {
	deadline := time.Now().Add(timeout)
	path := fmt.Sprintf("/admin/tenants/%s", slug)

	for {
		data, err := client.Get(cmd.Context(), path)
		if err != nil {
			return adminclient.WithJSONErrorEnvelope(cmd, err, jsonOut)
		}

		var resp tenantStatusResponse
		if err := json.Unmarshal(data, &resp); err != nil {
			return fmt.Errorf("decode tenant status response: %w", err)
		}

		if resp.Status == "active" {
			if jsonOut {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), string(data))
			} else {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "tenant %q is active\n", slug)
			}
			return nil
		}

		if time.Now().After(deadline) {
			return &clierr.Error{
				Code: 124,
				Err:  fmt.Errorf("tenant %q still %q after %s — check `goerp tenant status %s`", slug, resp.Status, timeout, slug),
			}
		}

		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "tenant %q still %s...\n", slug, resp.Status)

		select {
		case <-cmd.Context().Done():
			return cmd.Context().Err()
		case <-time.After(adminclient.PollInterval):
		}
	}
}
