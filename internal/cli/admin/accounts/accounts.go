// Package accounts provides operator controls for accounts across all tenants.
package accounts

import (
	"encoding/json/v2"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/djangbahevans/goerp/internal/cli/adminclient"
	"github.com/djangbahevans/goerp/internal/cli/clierr"
	"github.com/spf13/cobra"
)

func NewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "accounts",
		Short: "Manage accounts across every tenant",
	}

	cmd.AddCommand(
		newActionCmd("suspend", http.MethodPost, "/suspend", "suspended"),
		newActionCmd("unsuspend", http.MethodPost, "/unsuspend", "unsuspended"),
		newActionCmd("delete", http.MethodDelete, "", "deleted"),
		newActionCmd("mfa-reset", http.MethodPost, "/mfa/reset", "MFA reset"),
	)

	return cmd
}

func newActionCmd(action, method, suffix, result string) *cobra.Command {
	var reason, confirm string
	var dryRun bool

	destructive := action == "delete" || action == "mfa-reset"

	cmd := &cobra.Command{
		Use:   action + " <user-id>",
		Short: "Account " + action + " across every tenant",
		Args:  clierr.WrapArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]

			if strings.TrimSpace(id) == "" {
				return clierr.Usage(fmt.Errorf("user ID is required"))
			}

			if action == "suspend" && strings.TrimSpace(reason) == "" {
				return clierr.Usage(fmt.Errorf("--reason is required"))
			}

			if destructive && confirm != id {
				return clierr.Usage(fmt.Errorf("--confirm must exactly match %q", id))
			}

			client, err := adminclient.NewFromFlags(cmd)
			if err != nil {
				return err
			}

			path := "/admin/accounts/" + url.PathEscape(id) + suffix
			if dryRun {
				path += "?dry_run=true"
			}

			var body any
			if action == "suspend" {
				body = map[string]string{"reason": reason}
			}

			if dryRun {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "previewing %s for account %q across every tenant\n", action, id)
			} else {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "account %q: %s across every tenant\n", id, action)
			}

			data, err := client.Do(cmd.Context(), method, path, body)
			jsonOut, _ := cmd.Flags().GetBool("json")
			if err != nil {
				return adminclient.WithJSONErrorEnvelope(cmd, err, jsonOut)
			}

			if jsonOut {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), string(data))
				return nil
			}

			if dryRun {
				var preview struct {
					Email        string   `json:"email"`
					Tenants      []string `json:"tenants"`
					LiveSessions int      `json:"live_sessions"`
				}

				if err := json.Unmarshal(data, &preview); err != nil {
					return fmt.Errorf("decode account preview: %w", err)
				}

				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "would %s account %q (%s)\ntenants: %s\nlive sessions: %d\n", action, id, preview.Email, strings.Join(preview.Tenants, ", "), preview.LiveSessions)
				return nil
			}

			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "account %q: %s\n", id, result)
			return nil
		},
	}

	if action == "suspend" {
		cmd.Flags().StringVar(&reason, "reason", "", "Reason for suspension (required)")
	}

	if destructive {
		cmd.Flags().StringVar(&confirm, "confirm", "", "User ID confirmation (required)")
		cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview email, tenants and live sessions without changing the account")
	}

	return cmd
}
