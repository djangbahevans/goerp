// Package adminclient handles admin API envelopes and maps failures to CLI exit codes,
// keeping HTTP details out of individual commands.
package adminclient

import (
	"bytes"
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
	"uuid"

	"github.com/djangbahevans/goerp/internal/cli/clierr"
	"github.com/spf13/cobra"
)

// JSONEscapeOpts escapes JSON for safe embedding in HTML and JavaScript.
var JSONEscapeOpts = []json.Options{jsontext.EscapeForHTML(true), jsontext.EscapeForJS(true)}

type Client struct {
	baseURL string
	token   string
	http    *http.Client
}

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

// New requires both baseURL and token; missing either is a usage error.
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

func (c *Client) Get(ctx context.Context, path string) (jsontext.Value, error) {
	return c.Do(ctx, http.MethodGet, path, nil)
}

func (c *Client) Post(ctx context.Context, path string, body any) (jsontext.Value, error) {
	return c.Do(ctx, http.MethodPost, path, body)
}

// UploadFile spools a multipart upload to disk so retries can resend the same payload.
func (c *Client) UploadFile(ctx context.Context, path, fieldName, fileName string, r io.Reader) (jsontext.Value, error) {
	file, err := os.CreateTemp("", "goerp-admin-upload-*")
	if err != nil {
		return nil, fmt.Errorf("stage upload: %w", err)
	}
	defer func() {
		_ = file.Close()
		_ = os.Remove(file.Name())
	}()

	mw := multipart.NewWriter(file)
	part, err := mw.CreateFormFile(fieldName, fileName)
	if err != nil {
		return nil, fmt.Errorf("create upload field: %w", err)
	}
	if _, err := io.Copy(part, r); err != nil {
		return nil, fmt.Errorf("stage upload contents: %w", err)
	}
	if err := mw.Close(); err != nil {
		return nil, fmt.Errorf("finish upload: %w", err)
	}
	size, err := file.Seek(0, io.SeekCurrent)
	if err != nil {
		return nil, fmt.Errorf("measure upload: %w", err)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return nil, fmt.Errorf("rewind upload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, file)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.GetBody = func() (io.ReadCloser, error) { return os.Open(file.Name()) }
	req.ContentLength = size
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", mw.FormDataContentType())

	return c.send(req)
}

// PostBinary sends an unframed binary request body.
func (c *Client) PostBinary(ctx context.Context, path string, body []byte) (jsontext.Value, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/octet-stream")

	return c.send(req)
}

type envelope struct {
	Data  jsontext.Value `json:"data"`
	Error *apiErrorBody  `json:"error"`
}

type apiErrorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// APIError carries the admin error code, message and HTTP status.
type APIError struct {
	Code       string
	Message    string
	HTTPStatus int
	retryAfter time.Duration
}

func (e *APIError) Error() string {
	return e.Code + ": " + e.Message
}

// ErrorEnvelopeJSON reconstructs {"error":{"code":...,"message":...}}
// from err if it wraps an *APIError — canonical, marshaled fresh rather
// than passed through the server's raw bytes, so --json output never
// depends on exactly how the server formatted its response.
func ErrorEnvelopeJSON(err error) ([]byte, bool) {
	apiErr, ok := errors.AsType[*APIError](err)
	if !ok {
		return nil, false
	}

	out, marshalErr := json.Marshal(envelope{Error: &apiErrorBody{Code: apiErr.Code, Message: apiErr.Message}}, JSONEscapeOpts...)
	if marshalErr != nil {
		return nil, false
	}

	return out, true
}

// Do returns envelope data on success and a CLI exit error on failure.
func (c *Client) Do(ctx context.Context, method, path string, body any) (jsontext.Value, error) {
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

func (c *Client) send(req *http.Request) (jsontext.Value, error) {
	if req.Method != http.MethodGet && req.Method != http.MethodHead && req.Method != http.MethodOptions {
		req.Header.Set("Idempotency-Key", uuid.New().String())
	}

	var retryAfter time.Duration

	for attempt := range 3 {
		if attempt > 0 {
			timer := time.NewTimer(max(retryAfter, 250*time.Millisecond*time.Duration(1<<(attempt-1))))
			select {
			case <-req.Context().Done():
				timer.Stop()
				return nil, &clierr.Error{Code: 1, Err: req.Context().Err()}
			case <-timer.C:
			}

			if req.GetBody != nil {
				body, err := req.GetBody()
				if err != nil {
					return nil, &clierr.Error{Code: 1, Err: fmt.Errorf("reopen request body: %w", err)}
				}
				req.Body = body
			}
		}

		data, err := c.sendOnce(req)
		retryAfter = 0
		if apiErr, ok := errors.AsType[*APIError](err); ok {
			retryAfter = apiErr.retryAfter
		}
		if err == nil || attempt == 2 || req.Context().Err() != nil || (req.Body != nil && req.GetBody == nil) || !retryableRequestError(err) {
			return data, err
		}
	}
	panic("unreachable")
}

func retryableRequestError(err error) bool {
	if apiErr, ok := errors.AsType[*APIError](err); ok {
		return apiErr.HTTPStatus >= 500 || (apiErr.HTTPStatus == http.StatusConflict && apiErr.Code == "idempotency_in_progress")
	}
	_, ok := errors.AsType[net.Error](err)
	return ok
}

func (c *Client) sendOnce(req *http.Request) (jsontext.Value, error) {
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, &clierr.Error{Code: 1, Err: fmt.Errorf("admin API request failed: %w", err)}
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNoContent {
		return jsontext.Value("null"), nil
	}

	apiErr := &APIError{HTTPStatus: resp.StatusCode}
	if seconds, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && seconds > 0 {
		apiErr.retryAfter = time.Duration(min(seconds, 30)) * time.Second
	}

	var env envelope
	if err := json.UnmarshalRead(resp.Body, &env); err != nil {
		if resp.StatusCode >= 500 {
			apiErr.Code = "unknown"
			apiErr.Message = http.StatusText(resp.StatusCode)
			return nil, &clierr.Error{Code: 1, Err: apiErr}
		}
		return nil, &clierr.Error{Code: 1, Err: fmt.Errorf("decode admin API response: %w", err)}
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
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
var PollInterval = 2 * time.Second

// jobDetail is GET /admin/jobs/{id}'s response body, the slice every
// WaitForJob caller needs: State to know when to stop polling, Output to
// decode into the caller's own result type once it's completed.
type jobDetail struct {
	State  string         `json:"state"`
	Output jsontext.Value `json:"output,omitempty"`
}

// WaitForJob polls until a terminal River state, then decodes output into T. label
// identifies the operation in messages. Timeout returns CLI exit code 124.
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
