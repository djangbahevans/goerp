package authcheck

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWritePasswordChangeRequired(t *testing.T) {
	rec := httptest.NewRecorder()
	if !WritePasswordChangeRequired(t.Context(), rec, fmt.Errorf("authenticate: %w", ErrPasswordChangeRequired)) {
		t.Fatal("WritePasswordChangeRequired() = false for ErrPasswordChangeRequired, want true")
	}
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), `"password_change_required"`) {
		t.Errorf("status = %d, body = %s, want 403 password_change_required", rec.Code, rec.Body.String())
	}

	for _, err := range []error{nil, ErrInvalidToken, errors.New("boom")} {
		rec := httptest.NewRecorder()
		if WritePasswordChangeRequired(t.Context(), rec, err) {
			t.Errorf("WritePasswordChangeRequired(%v) = true, want false", err)
		}
		if rec.Body.Len() != 0 {
			t.Errorf("WritePasswordChangeRequired(%v) wrote %s, want nothing", err, rec.Body.String())
		}
	}
}
