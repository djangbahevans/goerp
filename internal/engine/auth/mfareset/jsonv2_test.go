package mfareset

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Body decoding follows tenant resolution and authentication, so malformed-body tests need
// an authenticated fixture.

func doRawReset(t *testing.T, f *fixture, accessToken string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/admin/users/some-id/mfa/reset", bytes.NewReader(body))
	req.Host = f.domain
	req.RemoteAddr = "203.0.113.7:54321"
	req.Header.Set("Authorization", "Bearer "+accessToken)
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	return rec
}

func TestServeHTTP_DuplicateObjectMemberNameIsBadRequest(t *testing.T) {
	f := newFixture(t)
	callerID := f.createCallerWithPassword(t)
	token := f.issueAccessToken(t, callerID)

	rec := doRawReset(t, f, token, []byte(`{"password":"a","password":"b"}`))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d (body: %s)", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "malformed request body") {
		t.Errorf("body = %s, want it to mention a malformed request body", rec.Body.String())
	}
}

func TestServeHTTP_InvalidUTF8IsBadRequest(t *testing.T) {
	f := newFixture(t)
	callerID := f.createCallerWithPassword(t)
	token := f.issueAccessToken(t, callerID)

	rec := doRawReset(t, f, token, []byte("{\"password\":\"\xff\xfe\"}"))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d (body: %s)", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "malformed request body") {
		t.Errorf("body = %s, want it to mention a malformed request body", rec.Body.String())
	}
}
