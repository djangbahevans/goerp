package adminclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/cli/clierr"
	"github.com/spf13/cobra"
)

// newTestCmd builds a bare *cobra.Command with a real background context
// and its own stdout/stderr buffers — WithJSONErrorEnvelope/WaitForJob
// only need cmd.Context()/cmd.OutOrStdout()/cmd.ErrOrStderr(), not a full
// command tree, unlike the black-box runCLI-based tests in internal/cli.
func newTestCmd() (cmd *cobra.Command, stdout, stderr *bytes.Buffer) {
	cmd = &cobra.Command{}
	cmd.SetContext(context.Background())
	stdout, stderr = &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	return cmd, stdout, stderr
}

func TestNew_MissingTokenIsUsageError(t *testing.T) {
	_, err := New("http://localhost:8081", "", time.Second)

	var ec clierr.ExitCoder
	if !errors.As(err, &ec) || ec.ExitCode() != 2 {
		t.Fatalf("New() with no token: error = %v, want exit code 2", err)
	}
}

func TestNew_MissingURLIsUsageError(t *testing.T) {
	_, err := New("", "sometoken", time.Second)

	var ec clierr.ExitCoder
	if !errors.As(err, &ec) || ec.ExitCode() != 2 {
		t.Fatalf("New() with no URL: error = %v, want exit code 2", err)
	}
}

func TestDo_SuccessReturnsData(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer testtoken" {
			t.Errorf("Authorization header = %q, want Bearer testtoken", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"slug":"acme"},"error":null}`))
	}))
	defer srv.Close()

	client, err := New(srv.URL, "testtoken", 5*time.Second)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	data, err := client.Get(context.Background(), "/admin/tenants/acme")
	if err != nil {
		t.Fatalf("Get() error: %v", err)
	}

	var got struct {
		Slug string `json:"slug"`
	}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal data: %v", err)
	}
	if got.Slug != "acme" {
		t.Errorf("Slug = %q, want %q", got.Slug, "acme")
	}
}

// TestDo_RejectsDuplicateObjectMemberNames and TestDo_RejectsInvalidUTF8
// pin encoding/json/v2's stricter decode of the admin API's own response
// envelope — the two behaviors goerp#520's ParseJSON migration established
// as the pattern, now that goerp#529 has moved internal/engine/adminapi's
// encoding to v2 too and this client no longer needs to stay lenient for
// it.
func TestDo_RejectsDuplicateObjectMemberNames(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"slug":"acme"},"data":{"slug":"acme"},"error":null}`))
	}))
	defer srv.Close()

	client, err := New(srv.URL, "testtoken", 5*time.Second)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	if _, err := client.Get(context.Background(), "/admin/tenants/acme"); err == nil {
		t.Fatal("Get() error = nil, want an error for a duplicate object member name")
	}
}

func TestDo_RejectsInvalidUTF8(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("{\"data\":{\"slug\":\"\xff\xfe\"},\"error\":null}"))
	}))
	defer srv.Close()

	client, err := New(srv.URL, "testtoken", 5*time.Second)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	if _, err := client.Get(context.Background(), "/admin/tenants/acme"); err == nil {
		t.Fatal("Get() error = nil, want an error for invalid UTF-8")
	}
}

func TestDo_ExitCodeMapping(t *testing.T) {
	cases := []struct {
		status   int
		wantCode int
	}{
		{http.StatusUnauthorized, 3},
		{http.StatusForbidden, 3},
		{http.StatusNotFound, 4},
		{http.StatusConflict, 5},
		{http.StatusInternalServerError, 1},
	}

	for _, c := range cases {
		t.Run(http.StatusText(c.status), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(c.status)
				_, _ = w.Write([]byte(`{"data":null,"error":{"code":"some_error","message":"boom"}}`))
			}))
			defer srv.Close()

			client, err := New(srv.URL, "testtoken", 5*time.Second)
			if err != nil {
				t.Fatalf("New() error: %v", err)
			}

			_, err = client.Get(context.Background(), "/admin/tenants/acme")

			var ec clierr.ExitCoder
			if !errors.As(err, &ec) {
				t.Fatalf("Get() error is not an ExitCoder: %v", err)
			}
			if ec.ExitCode() != c.wantCode {
				t.Errorf("ExitCode() = %d, want %d", ec.ExitCode(), c.wantCode)
			}

			var apiErr *APIError
			if !errors.As(err, &apiErr) {
				t.Fatalf("error does not wrap *APIError: %v", err)
			}
			if apiErr.Code != "some_error" || apiErr.Message != "boom" {
				t.Errorf("APIError = %+v, want code=some_error message=boom", apiErr)
			}
		})
	}
}

func TestDo_ConnectionFailureIsExitCode1(t *testing.T) {
	client, err := New("http://127.0.0.1:1", "testtoken", 200*time.Millisecond)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	_, err = client.Get(context.Background(), "/admin/tenants")

	var ec clierr.ExitCoder
	if !errors.As(err, &ec) || ec.ExitCode() != 1 {
		t.Fatalf("Get() against an unreachable server: error = %v, want exit code 1", err)
	}
}

func TestErrorEnvelopeJSON_RoundTrips(t *testing.T) {
	err := &clierr.Error{Code: 4, Err: &APIError{Code: "not_found", Message: "tenant not found", HTTPStatus: 404}}

	out, ok := ErrorEnvelopeJSON(err)
	if !ok {
		t.Fatal("ErrorEnvelopeJSON() ok = false, want true")
	}

	var env struct {
		Data  any `json:"data"`
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(out, &env); err != nil {
		t.Fatalf("unmarshal envelope: %v", err)
	}
	if env.Data != nil {
		t.Errorf("Data = %v, want nil", env.Data)
	}
	if env.Error.Code != "not_found" || env.Error.Message != "tenant not found" {
		t.Errorf("Error = %+v, want code=not_found message=%q", env.Error, "tenant not found")
	}
}

// TestErrorEnvelopeJSON_EscapesHTMLUnsafeCharacters pins JSONEscapeOpts:
// encoding/json/v2's Marshal doesn't apply v1's default HTML/JS-safe
// escaping on its own, so a server-supplied error message containing
// '<'/'>'/'&' must still come out escaped, matching the CLI's historical
// --json output bytes.
func TestErrorEnvelopeJSON_EscapesHTMLUnsafeCharacters(t *testing.T) {
	err := &clierr.Error{Code: 4, Err: &APIError{Code: "bad_input", Message: "value <b>&\"quoted\"</b> is invalid"}}

	out, ok := ErrorEnvelopeJSON(err)
	if !ok {
		t.Fatal("ErrorEnvelopeJSON() ok = false, want true")
	}

	if bytes.ContainsAny(out, "<>&") {
		t.Errorf("ErrorEnvelopeJSON() = %s, want '<', '>', '&' escaped as \\u00XX", out)
	}

	var env struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(out, &env); err != nil {
		t.Fatalf("unmarshal envelope: %v", err)
	}
	if want := `value <b>&"quoted"</b> is invalid`; env.Error.Message != want {
		t.Errorf("Error.Message = %q, want %q", env.Error.Message, want)
	}
}

func TestErrorEnvelopeJSON_NonAPIErrorReturnsFalse(t *testing.T) {
	_, ok := ErrorEnvelopeJSON(errors.New("some other error"))
	if ok {
		t.Error("ErrorEnvelopeJSON() ok = true for a non-APIError, want false")
	}
}

func TestWithJSONErrorEnvelope_PrintsEnvelopeWhenJSONOut(t *testing.T) {
	cmd, stdout, _ := newTestCmd()
	apiErr := &clierr.Error{Code: 4, Err: &APIError{Code: "not_found", Message: "tenant not found"}}

	got := WithJSONErrorEnvelope(cmd, apiErr, true)

	if got != apiErr {
		t.Errorf("WithJSONErrorEnvelope() = %v, want the same error returned unchanged", got)
	}
	if !bytes.Contains(stdout.Bytes(), []byte(`"not_found"`)) {
		t.Errorf("stdout = %q, want it to contain the error envelope", stdout.String())
	}
}

func TestWithJSONErrorEnvelope_SilentWhenNotJSONOut(t *testing.T) {
	cmd, stdout, _ := newTestCmd()
	apiErr := &clierr.Error{Code: 4, Err: &APIError{Code: "not_found", Message: "tenant not found"}}

	got := WithJSONErrorEnvelope(cmd, apiErr, false)

	if got != apiErr {
		t.Errorf("WithJSONErrorEnvelope() = %v, want the same error returned unchanged", got)
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want nothing printed when jsonOut is false", stdout.String())
	}
}

type waitForJobResult struct {
	Value string `json:"value"`
}

func TestWaitForJob_CompletedDecodesOutput(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"state":"completed","output":{"value":"done"}},"error":null}`))
	}))
	defer srv.Close()

	client, err := New(srv.URL, "testtoken", 5*time.Second)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	cmd, _, _ := newTestCmd()

	result, err := WaitForJob[waitForJobResult](cmd, client, "job_1", "test", time.Second)
	if err != nil {
		t.Fatalf("WaitForJob() error: %v", err)
	}
	if result.Value != "done" {
		t.Errorf("result.Value = %q, want %q", result.Value, "done")
	}
}

func TestWaitForJob_CancelledReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"state":"cancelled"},"error":null}`))
	}))
	defer srv.Close()

	client, err := New(srv.URL, "testtoken", 5*time.Second)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	cmd, _, _ := newTestCmd()

	_, err = WaitForJob[waitForJobResult](cmd, client, "job_2", "test", time.Second)
	if err == nil {
		t.Fatal("WaitForJob() error = nil, want an error for a cancelled job")
	}
}

func TestWaitForJob_TimeoutReturnsExitCode124(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"state":"running"},"error":null}`))
	}))
	defer srv.Close()

	client, err := New(srv.URL, "testtoken", 5*time.Second)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	cmd, _, _ := newTestCmd()

	_, err = WaitForJob[waitForJobResult](cmd, client, "job_3", "test", 50*time.Millisecond)

	var ec clierr.ExitCoder
	if !errors.As(err, &ec) || ec.ExitCode() != 124 {
		t.Fatalf("WaitForJob() error = %v, want exit code 124", err)
	}
}
