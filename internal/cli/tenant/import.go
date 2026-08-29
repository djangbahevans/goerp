package tenant

import (
	"archive/zip"
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/djangbahevans/goerp/internal/cli/adminclient"
	"github.com/djangbahevans/goerp/internal/cli/clierr"
	"github.com/spf13/cobra"
)

// importManifest mirrors tenantexport's (and tenantimport's) own archive
// manifest.json shape — the CLI stays decoupled from the engine binary's
// Go packages, communicating only over the wire, so this is a local copy
// of the wire format rather than an import (same convention exportResult
// in export.go already follows for tenantexport.Result).
type importManifest struct {
	TenantSlug string                 `json:"tenant_slug"`
	ExportedAt time.Time              `json:"exported_at"`
	Modules    []importManifestModule `json:"modules"`
}

type importManifestModule struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	File    string `json:"file"`
}

// importStartResponse mirrors POST /admin/tenants/import's response body.
type importStartResponse struct {
	JobID string `json:"job_id"`
}

// uploadResponse mirrors POST /admin/tenants/import/upload's response body.
type uploadResponse struct {
	InputRef string `json:"input_ref"`
}

// importJobDetail mirrors exportJobDetail (export.go) — the same GET
// /admin/jobs/{id} shape, decoded here for tenantimport.Result instead of
// tenantexport.Result.
type importJobDetail struct {
	State  string          `json:"state"`
	Output json.RawMessage `json:"output,omitempty"`
}

// importResult mirrors tenantimport.Result.
type importResult struct {
	TenantID        string   `json:"tenant_id"`
	TenantSlug      string   `json:"tenant_slug"`
	ModulesImported []string `json:"modules_imported"`
}

func newImportCmd() *cobra.Command {
	var input string
	var decryptionKey string
	var confirm string
	var dryRun bool
	var wait bool
	var waitTimeout time.Duration

	cmd := &cobra.Command{
		Use:   "import <slug>",
		Short: "Restore a tenant-export archive as a brand-new tenant",
		Args:  clierr.WrapArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			slug := args[0]
			if input == "" {
				return clierr.Usage(fmt.Errorf("--input is required"))
			}
			if decryptionKey == "" {
				return clierr.Usage(fmt.Errorf("--decryption-key is required"))
			}

			archiveBytes, err := os.ReadFile(input)
			if err != nil {
				return fmt.Errorf("read %s: %w", input, err)
			}

			jsonOut, _ := cmd.Flags().GetBool("json")

			if dryRun {
				return runImportDryRun(cmd, archiveBytes, decryptionKey, jsonOut)
			}

			if confirm != slug {
				return clierr.Usage(fmt.Errorf("--confirm must exactly match %q", slug))
			}

			client, err := adminclient.NewFromFlags(cmd)
			if err != nil {
				return err
			}

			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "uploading archive %s...\n", input)
			uploadData, err := client.UploadFile(cmd.Context(), "/admin/tenants/import/upload", "archive", filepath.Base(input), bytes.NewReader(archiveBytes))
			if err != nil {
				return withJSONErrorEnvelope(cmd, err, jsonOut)
			}
			var uploaded uploadResponse
			if err := json.Unmarshal(uploadData, &uploaded); err != nil {
				return fmt.Errorf("decode upload response: %w", err)
			}

			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "starting import for new tenant %q...\n", slug)
			data, err := client.Post(cmd.Context(), "/admin/tenants/import", map[string]any{
				"slug":           slug,
				"input":          uploaded.InputRef,
				"decryption_key": decryptionKey,
			})
			if err != nil {
				return withJSONErrorEnvelope(cmd, err, jsonOut)
			}

			var started importStartResponse
			if err := json.Unmarshal(data, &started); err != nil {
				return fmt.Errorf("decode import response: %w", err)
			}

			if !wait {
				if jsonOut {
					_, _ = fmt.Fprintln(cmd.OutOrStdout(), string(data))
					return nil
				}
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "import job started for tenant %q (job_id=%s)\n", slug, started.JobID)
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "check progress with: goerp jobs show %s\n", started.JobID)
				return nil
			}

			result, err := waitForImport(cmd, client, started.JobID, waitTimeout)
			if err != nil {
				return err
			}

			if jsonOut {
				out, err := json.Marshal(result)
				if err != nil {
					return fmt.Errorf("encode result: %w", err)
				}
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), string(out))
				return nil
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "tenant %q created from archive (modules: %v)\n", result.TenantSlug, result.ModulesImported)
			return nil
		},
	}

	cmd.Flags().StringVar(&input, "input", "", "Path to the encrypted archive to import (required)")
	cmd.Flags().StringVar(&decryptionKey, "decryption-key", "", "The archive's one-time decryption key, from `tenant export` (required)")
	cmd.Flags().StringVar(&confirm, "confirm", "", "Must pass the new tenant's slug as confirmation (required unless --dry-run)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Validate and report the archive's module/version manifest without creating a tenant")
	cmd.Flags().BoolVar(&wait, "wait", true, "Block until the import completes")
	cmd.Flags().DurationVar(&waitTimeout, "wait-timeout", 30*time.Minute, "How long --wait waits before giving up")

	return cmd
}

// withJSONErrorEnvelope mirrors export.go's own inline pattern for
// printing the reconstructed error envelope under --json before returning
// the error for exit-code handling.
func withJSONErrorEnvelope(cmd *cobra.Command, err error, jsonOut bool) error {
	if jsonOut {
		if envJSON, ok := adminclient.ErrorEnvelopeJSON(err); ok {
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), string(envJSON))
		}
	}
	return err
}

// runImportDryRun never calls the admin API — decrypting and parsing the
// local archive file entirely client-side is enough to report its
// module/version manifest, the same "no server-side dry-run support to
// call into" reasoning offboard.go's runOffboardDryRun documents for its
// own --dry-run.
func runImportDryRun(cmd *cobra.Command, archiveBytes []byte, decryptionKey string, jsonOut bool) error {
	man, err := decryptAndParseManifest(archiveBytes, decryptionKey)
	if err != nil {
		return err
	}

	if jsonOut {
		out, err := json.Marshal(man)
		if err != nil {
			return fmt.Errorf("encode manifest: %w", err)
		}
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), string(out))
		return nil
	}

	out := cmd.OutOrStdout()
	_, _ = fmt.Fprintf(out, "archive for tenant %q, exported %s:\n", man.TenantSlug, man.ExportedAt.Format(time.RFC3339))
	for _, m := range man.Modules {
		_, _ = fmt.Fprintf(out, "  %s@%s\n", m.Name, m.Version)
	}
	return nil
}

// decryptAndParseManifest reverses tenantexport's own AES-256-GCM
// encryption (nonce prepended to the ciphertext) and reads manifest.json
// out of the resulting zip — enough to report the archive's module/version
// set without needing the module data files at all.
func decryptAndParseManifest(ciphertext []byte, keyBase64 string) (importManifest, error) {
	key, err := base64.RawURLEncoding.DecodeString(keyBase64)
	if err != nil {
		return importManifest{}, fmt.Errorf("decode decryption key: %w", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return importManifest{}, fmt.Errorf("create AES cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return importManifest{}, fmt.Errorf("create GCM: %w", err)
	}
	nonceSize := gcm.NonceSize()
	if len(ciphertext) < nonceSize {
		return importManifest{}, fmt.Errorf("archive is too short to contain a nonce")
	}
	nonce, body := ciphertext[:nonceSize], ciphertext[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, body, nil)
	if err != nil {
		return importManifest{}, fmt.Errorf("decrypt archive (wrong key, or archive is corrupt/tampered): %w", err)
	}

	zr, err := zip.NewReader(bytes.NewReader(plaintext), int64(len(plaintext)))
	if err != nil {
		return importManifest{}, fmt.Errorf("open archive as zip: %w", err)
	}
	for _, f := range zr.File {
		if f.Name != "manifest.json" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return importManifest{}, fmt.Errorf("open manifest.json: %w", err)
		}
		data, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			return importManifest{}, fmt.Errorf("read manifest.json: %w", err)
		}
		var man importManifest
		if err := json.Unmarshal(data, &man); err != nil {
			return importManifest{}, fmt.Errorf("decode manifest.json: %w", err)
		}
		return man, nil
	}
	return importManifest{}, fmt.Errorf("archive has no manifest.json")
}

// waitForImport polls GET /admin/jobs/{id} until the job reaches a
// terminal River state — mirrors export.go's waitForExport exactly, for
// tenantimport.Result instead of tenantexport.Result.
func waitForImport(cmd *cobra.Command, client *adminclient.Client, jobID string, timeout time.Duration) (importResult, error) {
	deadline := time.Now().Add(timeout)
	path := "/admin/jobs/" + jobID

	for {
		data, err := client.Get(cmd.Context(), path)
		if err != nil {
			return importResult{}, err
		}

		var detail importJobDetail
		if err := json.Unmarshal(data, &detail); err != nil {
			return importResult{}, fmt.Errorf("decode job detail response: %w", err)
		}

		switch detail.State {
		case "completed":
			if len(detail.Output) == 0 {
				return importResult{}, fmt.Errorf("import job %s completed with no recorded output", jobID)
			}
			var result importResult
			if err := json.Unmarshal(detail.Output, &result); err != nil {
				return importResult{}, fmt.Errorf("decode import result: %w", err)
			}
			return result, nil
		case "cancelled", "discarded":
			return importResult{}, fmt.Errorf("import job %s did not complete (state=%s) — check `goerp jobs show %s --logs`", jobID, detail.State, jobID)
		}

		if time.Now().After(deadline) {
			return importResult{}, fmt.Errorf("import job %s still %q after %s — check `goerp jobs show %s`", jobID, detail.State, timeout, jobID)
		}

		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "import job %s still %s...\n", jobID, detail.State)

		select {
		case <-cmd.Context().Done():
			return importResult{}, cmd.Context().Err()
		case <-time.After(pollInterval):
		}
	}
}
