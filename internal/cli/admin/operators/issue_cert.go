package operators

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/djangbahevans/goerp/internal/cli/adminclient"
	"github.com/djangbahevans/goerp/internal/cli/clierr"
	"github.com/spf13/cobra"
)

type issueCertResponse struct {
	Certificate  string `json:"certificate"`
	PrivateKey   string `json:"private_key"`
	SerialNumber string `json:"serial_number"`
}

func newIssueCertCmd() *cobra.Command {
	var name, expires, output string

	cmd := &cobra.Command{
		Use:   "issue-cert",
		Short: "Issue a new operator mTLS client certificate for the admin gateway",
		RunE: func(cmd *cobra.Command, args []string) error {
			if name == "" {
				return clierr.Usage(fmt.Errorf("--name is required"))
			}

			client, err := adminclient.NewFromFlags(cmd)
			if err != nil {
				return err
			}

			data, err := client.Post(cmd.Context(), "/admin/operators/issue-cert", map[string]string{
				"name":    name,
				"expires": expires,
			})

			jsonOut, _ := cmd.Flags().GetBool("json")
			if err != nil {
				return adminclient.WithJSONErrorEnvelope(cmd, err, jsonOut)
			}

			if jsonOut {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), string(data))
				return nil
			}

			var resp issueCertResponse
			if err := json.Unmarshal(data, &resp); err != nil {
				return fmt.Errorf("decode issue-cert response: %w", err)
			}

			outputDir := output
			if outputDir == "" {
				outputDir = "."
			}
			if err := os.MkdirAll(outputDir, 0o700); err != nil {
				return fmt.Errorf("create output directory: %w", err)
			}

			certPath := filepath.Join(outputDir, name+".crt")
			keyPath := filepath.Join(outputDir, name+".key")

			if err := os.WriteFile(certPath, []byte(resp.Certificate), 0o600); err != nil {
				return fmt.Errorf("write certificate: %w", err)
			}
			if err := os.WriteFile(keyPath, []byte(resp.PrivateKey), 0o600); err != nil {
				return fmt.Errorf("write private key: %w", err)
			}

			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "issued certificate for %q (serial %s)\n  cert: %s\n  key:  %s\n",
				name, resp.SerialNumber, certPath, keyPath)

			return nil
		},
	}

	cmd.Flags().StringVar(&name, "name", "", "Operator or system name — becomes the certificate's CN (required)")
	cmd.Flags().StringVar(&expires, "expires", "90d", "Certificate lifetime")
	cmd.Flags().StringVar(&output, "output", "", "Directory to write the issued cert+key to (default: current directory)")

	return cmd
}
