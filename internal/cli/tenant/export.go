package tenant

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/djangbahevans/goerp/internal/cli/adminclient"
	"github.com/djangbahevans/goerp/internal/cli/clierr"
	"github.com/spf13/cobra"
)

// exportStartResponse mirrors POST /admin/tenants/{slug}/export's
// response body (internal/engine/adminapi/tenant.go's export handler).
type exportStartResponse struct {
	JobID string `json:"job_id"`
}

// exportJobDetail is the slice of GET /admin/jobs/{id}'s response this
// command needs — State to know when to stop polling, Output to decode
// the export result once it's completed (internal/engine/adminapi/jobs.go's
// jobDetailView, internal/engine/tenant/export.Result).
type exportJobDetail struct {
	State  string          `json:"state"`
	Output json.RawMessage `json:"output,omitempty"`
}

// exportResult mirrors tenantexport.Result — the one place the archive's
// one-time decryption key is ever returned.
type exportResult struct {
	DownloadURL   string `json:"download_url"`
	Checksum      string `json:"checksum_sha256"`
	DecryptionKey string `json:"decryption_key"`
}

func newExportCmd() *cobra.Command {
	var output string
	var include []string
	var exclude []string
	var wait bool
	var waitTimeout time.Duration

	cmd := &cobra.Command{
		Use:   "export <slug>",
		Short: "Export a tenant's schema and data to an AES-256-GCM-encrypted archive",
		Args:  clierr.WrapArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			slug := args[0]
			if output == "" {
				return clierr.Usage(fmt.Errorf("--output is required"))
			}

			client, err := adminclient.NewFromFlags(cmd)
			if err != nil {
				return err
			}

			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "starting export for tenant %q...\n", slug)

			data, err := client.Post(cmd.Context(), fmt.Sprintf("/admin/tenants/%s/export", slug), map[string]any{
				"include": include,
				"exclude": exclude,
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

			var started exportStartResponse
			if err := json.Unmarshal(data, &started); err != nil {
				return fmt.Errorf("decode export response: %w", err)
			}

			if !wait {
				if jsonOut {
					_, _ = fmt.Fprintln(cmd.OutOrStdout(), string(data))
					return nil
				}
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "export job started for tenant %q (job_id=%s)\n", slug, started.JobID)
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "check progress with: goerp jobs show %s\n", started.JobID)
				return nil
			}

			result, err := waitForExport(cmd, client, started.JobID, waitTimeout)
			if err != nil {
				return err
			}

			return downloadAndVerifyExport(cmd, result, output, jsonOut)
		},
	}

	cmd.Flags().StringVar(&output, "output", "", "Path to write the encrypted archive to (required)")
	cmd.Flags().StringSliceVar(&include, "include", nil, "Only export these modules")
	cmd.Flags().StringSliceVar(&exclude, "exclude", nil, "Exclude these modules from the export")
	cmd.Flags().BoolVar(&wait, "wait", true, "Block until the export completes, then download and verify the archive")
	cmd.Flags().DurationVar(&waitTimeout, "wait-timeout", 30*time.Minute, "How long --wait waits before giving up")

	return cmd
}

// waitForExport polls GET /admin/jobs/{id} (the same request `jobs show`
// makes) until the job reaches a terminal River state.
func waitForExport(cmd *cobra.Command, client *adminclient.Client, jobID string, timeout time.Duration) (exportResult, error) {
	deadline := time.Now().Add(timeout)
	path := "/admin/jobs/" + jobID

	for {
		data, err := client.Get(cmd.Context(), path)
		if err != nil {
			return exportResult{}, err
		}

		var detail exportJobDetail
		if err := json.Unmarshal(data, &detail); err != nil {
			return exportResult{}, fmt.Errorf("decode job detail response: %w", err)
		}

		switch detail.State {
		case "completed":
			if len(detail.Output) == 0 {
				return exportResult{}, fmt.Errorf("export job %s completed with no recorded output", jobID)
			}
			var result exportResult
			if err := json.Unmarshal(detail.Output, &result); err != nil {
				return exportResult{}, fmt.Errorf("decode export result: %w", err)
			}
			return result, nil
		case "cancelled", "discarded":
			return exportResult{}, fmt.Errorf("export job %s did not complete (state=%s) — check `goerp jobs show %s --logs`", jobID, detail.State, jobID)
		}

		if time.Now().After(deadline) {
			return exportResult{}, fmt.Errorf("export job %s still %q after %s — check `goerp jobs show %s`", jobID, detail.State, timeout, jobID)
		}

		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "export job %s still %s...\n", jobID, detail.State)

		select {
		case <-cmd.Context().Done():
			return exportResult{}, cmd.Context().Err()
		case <-time.After(pollInterval):
		}
	}
}

// downloadAndVerifyExport fetches the archive directly from its signed
// object-storage URL — not through adminclient.Client, since that URL is
// pre-authorized by object storage itself and carries no admin bearer
// token — verifies its checksum, and writes it to outputPath still
// encrypted (cli-reference.md §5: the CLI prints the one-time decryption
// key, it doesn't decrypt automatically).
func downloadAndVerifyExport(cmd *cobra.Command, result exportResult, outputPath string, jsonOut bool) error {
	req, err := http.NewRequestWithContext(cmd.Context(), http.MethodGet, result.DownloadURL, nil)
	if err != nil {
		return fmt.Errorf("build download request: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("download archive: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download archive: unexpected status %s", resp.Status)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read archive: %w", err)
	}

	sum := sha256.Sum256(data)
	if got := hex.EncodeToString(sum[:]); got != result.Checksum {
		return fmt.Errorf("checksum mismatch: got %s, want %s — archive may be corrupt", got, result.Checksum)
	}

	if err := os.WriteFile(outputPath, data, 0o600); err != nil {
		return fmt.Errorf("write archive to %s: %w", outputPath, err)
	}

	if jsonOut {
		out, err := json.Marshal(map[string]string{
			"output":          outputPath,
			"checksum_sha256": result.Checksum,
			"decryption_key":  result.DecryptionKey,
		})
		if err != nil {
			return fmt.Errorf("encode result: %w", err)
		}
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), string(out))
		return nil
	}

	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "archive written to %s (checksum verified)\n", outputPath)
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "decryption key (save this — it is never shown again): %s\n", result.DecryptionKey)
	return nil
}
