package module

import (
	"encoding/json/v2"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/djangbahevans/goerp/internal/cli/adminclient"
	"github.com/djangbahevans/goerp/internal/cli/clierr"
	"github.com/spf13/cobra"
)

// installStartResponse mirrors POST /admin/modules/install's response body.
type installStartResponse struct {
	JobID string `json:"job_id"`
}

// installTenantResult mirrors moduleinstall.TenantResult.
type installTenantResult struct {
	Tenant string `json:"tenant"`
	Error  string `json:"error,omitempty"`
}

// installResult mirrors moduleinstall.Result — the CLI stays decoupled
// from the engine binary's Go packages, communicating only over the
// wire, so this is a local copy of the wire format rather than an
// import (same convention tenant/import.go's importResult follows for
// tenantimport.Result).
type installResult struct {
	Module          string                `json:"module"`
	Version         string                `json:"version"`
	Succeeded       []string              `json:"succeeded_tenants"`
	Failed          []installTenantResult `json:"failed_tenants"`
	WorkflowWorkers string                `json:"workflow_worker_error,omitempty"`
}

func newInstallCmd() *cobra.Command {
	var wait bool
	var timeout time.Duration
	var skipSignatureVerification bool

	cmd := &cobra.Command{
		Use:   "install <path-or-name>",
		Short: "Submit a .erp package to a running engine for installation",
		Args:  clierr.WrapArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			target := args[0]
			jsonOut, _ := cmd.Flags().GetBool("json")

			if _, statErr := os.Stat(target); statErr != nil {
				if strings.Contains(target, "@") && !strings.HasSuffix(target, ".erp") {
					return clierr.Usage(fmt.Errorf("install by registry reference (%q) is not yet supported — pass a local .erp file path (tracked as goerp#563)", target))
				}
				return fmt.Errorf("read %s: %w", target, statErr)
			}

			pkgBytes, err := os.ReadFile(target)
			if err != nil {
				return fmt.Errorf("read %s: %w", target, err)
			}

			client, err := adminclient.NewFromFlags(cmd)
			if err != nil {
				return err
			}

			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "uploading %s...\n", target)
			data, err := client.PostBinary(cmd.Context(), "/admin/modules/install", pkgBytes)
			if err != nil {
				return adminclient.WithJSONErrorEnvelope(cmd, err, jsonOut)
			}

			var started installStartResponse
			if err := json.Unmarshal(data, &started); err != nil {
				return fmt.Errorf("decode install response: %w", err)
			}

			if !wait {
				if jsonOut {
					_, _ = fmt.Fprintln(cmd.OutOrStdout(), string(data))
					return nil
				}
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "install job started (job_id=%s)\n", started.JobID)
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "check progress with: goerp jobs show %s\n", started.JobID)
				return nil
			}

			result, err := adminclient.WaitForJob[installResult](cmd, client, started.JobID, "install", timeout)
			if err != nil {
				return err
			}

			if jsonOut {
				out, err := json.Marshal(result, adminclient.JSONEscapeOpts...)
				if err != nil {
					return fmt.Errorf("encode result: %w", err)
				}
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), string(out))
				return nil
			}

			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "installed %s@%s (succeeded: %d tenant(s), failed: %d tenant(s))\n",
				result.Module, result.Version, len(result.Succeeded), len(result.Failed))
			for _, f := range result.Failed {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  failed: %s: %s\n", f.Tenant, f.Error)
			}
			if result.WorkflowWorkers != "" {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "workflow worker warning: %s\n", result.WorkflowWorkers)
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&wait, "wait", true, "Block until schema sync completes")
	cmd.Flags().DurationVar(&timeout, "timeout", 5*time.Minute, "Schema sync timeout")
	cmd.Flags().BoolVar(&skipSignatureVerification, "skip-signature-verification", false, "Dev only — accepted for forward compatibility; currently a no-op, since the engine performs no signature check yet (goerp#563)")

	return cmd
}
