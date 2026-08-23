package engine

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRecoveryMiddleware_RecoversPanicReturns500WithoutLeakingDetails(t *testing.T) {
	h := recoveryMiddleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("something exploded: super-secret internal detail")
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()

	// The test itself must not panic — recover() inside the middleware
	// should stop it from propagating up through ServeHTTP.
	h.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", w.Code)
	}

	rawBody := w.Body.String()
	if rawBody == "" {
		t.Fatal("empty response body")
	}

	var body routeErrorEnvelope
	if err := json.Unmarshal([]byte(rawBody), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Error.Code != "internal_error" {
		t.Errorf("error.code = %q, want %q", body.Error.Code, "internal_error")
	}
	if strings.Contains(rawBody, "super-secret internal detail") {
		t.Errorf("response body leaked the panic message: %s", rawBody)
	}
	if strings.Contains(rawBody, "goroutine") {
		t.Errorf("response body leaked a stack trace: %s", rawBody)
	}
}

func TestRecoveryMiddleware_NoPanicPassesThroughUnaffected(t *testing.T) {
	h := recoveryMiddleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
		_, _ = w.Write([]byte("i am a teapot"))
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusTeapot {
		t.Errorf("status = %d, want %d", w.Code, http.StatusTeapot)
	}
	if w.Body.String() != "i am a teapot" {
		t.Errorf("body = %q, want %q", w.Body.String(), "i am a teapot")
	}
}
