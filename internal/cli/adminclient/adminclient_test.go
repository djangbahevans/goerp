package adminclient

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/cli/clierr"
)

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

func TestErrorEnvelopeJSON_NonAPIErrorReturnsFalse(t *testing.T) {
	_, ok := ErrorEnvelopeJSON(errors.New("some other error"))
	if ok {
		t.Error("ErrorEnvelopeJSON() ok = true for a non-APIError, want false")
	}
}
