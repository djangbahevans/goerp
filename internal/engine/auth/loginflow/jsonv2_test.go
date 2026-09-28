package loginflow

import (
	"encoding/json/v2"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/djangbahevans/goerp/internal/engine/httperr"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// encoding/json/v2 matches JSON field names to Go struct fields
// case-sensitively, with no v1-style case-insensitive fallback — a
// case-mismatched key is unknown, not matched, and left at its zero
// value rather than erroring. Intentional (goerp#520/#529/#530); this
// makes that behavior explicit for loginRequest specifically.
func TestLoginRequestDecode_CaseMismatchedFieldNamesAreUnknownNotMatched(t *testing.T) {
	var req loginRequest
	if err := json.Unmarshal([]byte(`{"Email":"a@b.com","Password":"x","Tenant":"acme","Device_id":"d"}`), &req); err != nil {
		t.Fatalf("Unmarshal() error: %v", err)
	}
	if req.Email != "" || req.Password != "" || req.Tenant != "" || req.DeviceID != "" {
		t.Errorf("req = %+v, want every field left at its zero value", req)
	}
}

// The next three exercise encoding/json/v2's stricter decode defaults
// (goerp#530) through the real handler, before any dependency is
// touched — decode happens first in ServeHTTP, so a zero-valued Handler
// is enough.

func TestServeHTTP_DuplicateObjectMemberNameIsBadRequest(t *testing.T) {
	h := &Handler{}
	req := httptest.NewRequest("POST", "/auth/login", strings.NewReader(`{"email":"a@b.com","email":"c@d.com","password":"x","tenant":"acme"}`))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != 400 {
		t.Fatalf("status = %d, want 400 (body: %s)", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "malformed request body") {
		t.Errorf("body = %s, want it to mention a malformed request body", w.Body.String())
	}
}

func TestServeHTTP_InvalidUTF8IsBadRequest(t *testing.T) {
	h := &Handler{}
	req := httptest.NewRequest("POST", "/auth/login", strings.NewReader("{\"email\":\"\xff\xfe\",\"password\":\"x\",\"tenant\":\"acme\"}"))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != 400 {
		t.Fatalf("status = %d, want 400 (body: %s)", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "malformed request body") {
		t.Errorf("body = %s, want it to mention a malformed request body", w.Body.String())
	}
}

// An auth handler's error envelope carries the request's request_id and
// trace_id through the shared writer (goerp#1094).
func TestServeHTTP_ErrorEnvelopeCarriesRequestAndTraceID(t *testing.T) {
	tp := sdktrace.NewTracerProvider()
	t.Cleanup(func() { _ = tp.Shutdown(t.Context()) })
	ctx, span := tp.Tracer("test").Start(httperr.WithRequestID(t.Context(), "req-1"), "POST /auth/login")
	defer span.End()

	h := &Handler{}
	req := httptest.NewRequestWithContext(ctx, "POST", "/auth/login", strings.NewReader(`{`))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	var env httperr.Envelope
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode %s: %v", w.Body.String(), err)
	}
	if env.Error.RequestID != "req-1" {
		t.Errorf("request_id = %q, want %q", env.Error.RequestID, "req-1")
	}
	if want := span.SpanContext().TraceID().String(); env.Error.TraceID != want {
		t.Errorf("trace_id = %q, want %q", env.Error.TraceID, want)
	}
}
