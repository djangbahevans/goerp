package loginsession

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClearCookies_ExpiresAccessAndRefreshCookies(t *testing.T) {
	w := httptest.NewRecorder()
	ClearCookies(w)

	byName := map[string]*http.Cookie{}
	for _, c := range w.Result().Cookies() {
		byName[c.Name] = c
	}

	access, ok := byName["__Host-access_token"]
	if !ok {
		t.Fatal("__Host-access_token cookie not set")
	}
	if access.Path != "/" {
		t.Errorf("__Host-access_token Path = %q, want %q (must match setCookies' or the browser won't clear it)", access.Path, "/")
	}
	if access.MaxAge >= 0 {
		t.Errorf("__Host-access_token MaxAge = %d, want negative", access.MaxAge)
	}

	refresh, ok := byName["refresh_token"]
	if !ok {
		t.Fatal("refresh_token cookie not set")
	}
	if refresh.Path != "/auth/refresh" {
		t.Errorf("refresh_token Path = %q, want %q (must match setCookies' or the browser won't clear it)", refresh.Path, "/auth/refresh")
	}
	if refresh.MaxAge >= 0 {
		t.Errorf("refresh_token MaxAge = %d, want negative", refresh.MaxAge)
	}

	if _, ok := byName["device_id"]; ok {
		t.Error("device_id cookie was set/cleared, want it left alone — it identifies the device across logins, not this session")
	}
}
