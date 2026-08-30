// Package adminclient is every admin-facing CLI command's connection to
// /admin/* (cli-reference.md §1a/§2b) — parses the admin API's own
// {"data":...,"error":...} envelope (engine-internals.md §11) and maps
// failures onto the CLI's documented exit codes (§2b), so individual
// commands never touch net/http or the envelope shape directly.
package adminclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"time"

	"github.com/djangbahevans/goerp/internal/cli/clierr"
	"github.com/spf13/cobra"
)

type Client struct {
	baseURL string
	token   string
	http    *http.Client
}

// NewFromFlags reads --admin-url/--admin-token/--timeout off cmd —
// inherited persistent flags resolve correctly via cmd.Flags() once
// Cobra has merged them, the same pattern every subcommand RunE already
// relies on for flags declared on the root command.
func NewFromFlags(cmd *cobra.Command) (*Client, error) {
	adminURL, err := cmd.Flags().GetString("admin-url")
	if err != nil {
		return nil, err
	}
	adminToken, err := cmd.Flags().GetString("admin-token")
	if err != nil {
		return nil, err
	}
	timeout, err := cmd.Flags().GetDuration("timeout")
	if err != nil {
		return nil, err
	}

	return New(adminURL, adminToken, timeout)
}

// New requires baseURL and token together — there's no --env-resolved
// fallback yet (.goerp.toml resolution is separate, unfiled scope), so
// missing either (or both) is a usage error rather than a silent partial
// connection, per cli-reference.md §2b.
func New(baseURL, token string, timeout time.Duration) (*Client, error) {
	if baseURL == "" || token == "" {
		return nil, clierr.Usage(fmt.Errorf("--admin-url and --admin-token are required together"))
	}

	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		http:    &http.Client{Timeout: timeout},
	}, nil
}

func (c *Client) Get(ctx context.Context, path string) (json.RawMessage, error) {
	return c.Do(ctx, http.MethodGet, path, nil)
}

func (c *Client) Post(ctx context.Context, path string, body any) (json.RawMessage, error) {
	return c.Do(ctx, http.MethodPost, path, body)
}

// UploadFile POSTs r's contents as a multipart/form-data field named
// fieldName to path — `tenant import`'s only use, uploading the local
// archive file so the admin API can stage it in object storage before
// StartImport (POST /admin/tenants/import) is called with the returned
// key. Streamed via an io.Pipe so an arbitrarily large archive is never
// buffered whole in memory, the same reason downloadAndVerifyExport
// streams the export path's own archive straight to disk.
func (c *Client) UploadFile(ctx context.Context, path, fieldName, fileName string, r io.Reader) (json.RawMessage, error) {
	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)

	go func() {
		part, err := mw.CreateFormFile(fieldName, fileName)
		if err != nil {
			_ = pw.CloseWithError(err)
			return
		}
		if _, err := io.Copy(part, r); err != nil {
			_ = pw.CloseWithError(err)
			return
		}
		_ = pw.CloseWithError(mw.Close())
	}()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, pr)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", mw.FormDataContentType())

	return c.send(req)
}

// PostBinary POSTs body as-is with no JSON/multipart framing — `module
// install`'s own contract per POST /admin/modules/install, which reads
// the request body directly as the .erp package rather than expecting a
// JSON envelope or a multipart form.
func (c *Client) PostBinary(ctx context.Context, path string, body []byte) (json.RawMessage, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/octet-stream")

	return c.send(req)
}

type envelope struct {
	Data  json.RawMessage `json:"data"`
	Error *apiErrorBody   `json:"error"`
}

type apiErrorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// APIError is the admin API's own {"code":...,"message":...} error,
// carried alongside the HTTP status it arrived with so Do's caller (via
// exitCodeForStatus) already knows the right exit code without
// re-inspecting the response itself.
type APIError struct {
	Code       string
	Message    string
	HTTPStatus int
}

func (e *APIError) Error() string {
	return e.Code + ": " + e.Message
}

// ErrorEnvelopeJSON reconstructs {"error":{"code":...,"message":...}}
// from err if it wraps an *APIError — canonical, marshaled fresh rather
// than passed through the server's raw bytes, so --json output never
// depends on exactly how the server formatted its response.
func ErrorEnvelopeJSON(err error) ([]byte, bool) {
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		return nil, false
	}

	out, marshalErr := json.Marshal(envelope{Error: &apiErrorBody{Code: apiErr.Code, Message: apiErr.Message}})
	if marshalErr != nil {
		return nil, false
	}

	return out, true
}

// Do sends a request and returns the envelope's data field verbatim on a
// 2xx response. On failure it returns a *clierr.Error whose Code is
// already mapped from the HTTP status (exitCodeForStatus) and whose Err
// is an *APIError carrying the admin API's own code/message — callers
// just `return err`; root.go's existing ExitCoder handling does the rest.
func (c *Client) Do(ctx context.Context, method, path string, body any) (json.RawMessage, error) {
	var bodyReader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("encode request body: %w", err)
		}
		bodyReader = bytes.NewReader(encoded)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	return c.send(req)
}

// send issues req and decodes the admin API's envelope response — the
// part Do and UploadFile share once their own request bodies are built.
func (c *Client) send(req *http.Request) (json.RawMessage, error) {
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, &clierr.Error{Code: 1, Err: fmt.Errorf("admin API request failed: %w", err)}
	}
	defer func() { _ = resp.Body.Close() }()

	var env envelope
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		return nil, &clierr.Error{Code: 1, Err: fmt.Errorf("decode admin API response: %w", err)}
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		apiErr := &APIError{HTTPStatus: resp.StatusCode}
		if env.Error != nil {
			apiErr.Code = env.Error.Code
			apiErr.Message = env.Error.Message
		} else {
			apiErr.Code = "unknown"
			apiErr.Message = fmt.Sprintf("admin API returned status %d", resp.StatusCode)
		}
		return nil, &clierr.Error{Code: exitCodeForStatus(resp.StatusCode), Err: apiErr}
	}

	return env.Data, nil
}

// exitCodeForStatus maps an admin API failure onto cli-reference.md §2b's
// documented exit codes.
func exitCodeForStatus(status int) int {
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		return 3
	case http.StatusNotFound:
		return 4
	case http.StatusConflict:
		return 5
	default:
		return 1
	}
}

// WithJSONErrorEnvelope prints err's reconstructed {"error":...} envelope
// to cmd's stdout when jsonOut is set (cli-reference.md §2b: a script
// reading --json output needs a JSON body on failure too, not just a
// stderr message), then returns err unchanged so the caller can just
// `return WithJSONErrorEnvelope(cmd, err, jsonOut)`.
func WithJSONErrorEnvelope(cmd *cobra.Command, err error, jsonOut bool) error {
	if jsonOut {
		if envJSON, ok := ErrorEnvelopeJSON(err); ok {
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), string(envJSON))
		}
	}
	return err
}

// PollInterval is how often WaitForJob re-polls GET /admin/jobs/{id}
// while a job is still running.
const PollInterval = 2 * time.Second

// jobDetail is GET /admin/jobs/{id}'s response body, the slice every
// WaitForJob caller needs: State to know when to stop polling, Output to
// decode into the caller's own result type once it's completed.
type jobDetail struct {
	State  string          `json:"state"`
	Output json.RawMessage `json:"output,omitempty"`
}

// WaitForJob polls GET /admin/jobs/{id} until jobID reaches a terminal
// River state, then decodes its output into T. label names the kind of
// job in progress/error messages (e.g. "export", "import", "schema
// sync"). Returns a *clierr.Error{Code: 124} once timeout elapses —
// cli-reference.md §2b's documented exit code for a `--timeout`-bounded
// wait that never reached a terminal state.
func WaitForJob[T any](cmd *cobra.Command, client *Client, jobID, label string, timeout time.Duration) (T, error) {
	var zero T
	deadline := time.Now().Add(timeout)
	path := "/admin/jobs/" + jobID

	for {
		data, err := client.Get(cmd.Context(), path)
		if err != nil {
			return zero, err
		}

		var detail jobDetail
		if err := json.Unmarshal(data, &detail); err != nil {
			return zero, fmt.Errorf("decode job detail response: %w", err)
		}

		switch detail.State {
		case "completed":
			if len(detail.Output) == 0 {
				return zero, fmt.Errorf("%s job %s completed with no recorded output", label, jobID)
			}
			var result T
			if err := json.Unmarshal(detail.Output, &result); err != nil {
				return zero, fmt.Errorf("decode %s result: %w", label, err)
			}
			return result, nil
		case "cancelled", "discarded":
			return zero, fmt.Errorf("%s job %s did not complete (state=%s) — check `goerp jobs show %s --logs`", label, jobID, detail.State, jobID)
		}

		if time.Now().After(deadline) {
			return zero, &clierr.Error{
				Code: 124,
				Err:  fmt.Errorf("%s job %s still %q after %s — check `goerp jobs show %s`", label, jobID, detail.State, timeout, jobID),
			}
		}

		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "%s job %s still %s...\n", label, jobID, detail.State)

		select {
		case <-cmd.Context().Done():
			return zero, cmd.Context().Err()
		case <-time.After(PollInterval):
		}
	}
}
