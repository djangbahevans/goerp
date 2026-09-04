// Package loginsession implements the final "issue a browser or
// non-browser session response" step shared by every endpoint that ends
// in a full session issuance — POST /auth/login (auth-internals.md §3
// step 11) and POST /auth/mfa/verify (§8 step 7) both reach this same
// point once the caller is fully authenticated. Device ID resolution,
// the browser/non-browser response shape split, and the login cookies
// themselves are identical in both places, so this package holds the one
// copy both handlers call.
package loginsession

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"net"
	"net/http"

	"github.com/google/uuid"

	"github.com/djangbahevans/goerp/internal/engine/auth/authtoken"
)

// ResolveDeviceID returns the effective device_id and whether it was
// freshly generated (never seen before this request) — a web client's
// existing device_id travels as a cookie, a non-browser client's as an
// explicit body field, per auth-internals.md §3's "non-browser clients
// only" annotation on the login request body's device_id field.
func ResolveDeviceID(r *http.Request, bodyDeviceID string, nonBrowser bool) (id string, isFresh bool) {
	var candidate string
	if nonBrowser {
		candidate = bodyDeviceID
	} else if cookie, err := r.Cookie("device_id"); err == nil {
		candidate = cookie.Value
	}

	if candidate != "" {
		if _, err := uuid.Parse(candidate); err == nil {
			return candidate, false
		}
	}
	return uuid.NewString(), true
}

// ClientIP extracts the request's remote address, stripping the port.
// Real-IP resolution behind a proxy (X-Forwarded-For, etc.) is goerp#91's
// own scope (the middleware chain's "real IP resolution" step) — this is
// the unproxied fallback until that lands.
func ClientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// IsNonBrowser reports whether the request identifies itself as a
// non-browser client via the X-Client-Type header — auth-internals.md §3
// "Distinguishing web and non-browser clients": absence defaults to the
// safe, cookie-only browser path.
func IsNonBrowser(r *http.Request) bool {
	return r.Header.Get("X-Client-Type") == "cli"
}

// writeJSON matches encoding/json v1's Encoder defaults, which
// json.MarshalWrite doesn't apply on its own: '<', '>', '&' escaped for
// safe HTML embedding, and U+2028/U+2029 escaped for safe JS embedding.
func writeJSON(w http.ResponseWriter, v any) {
	_ = json.MarshalWrite(w, v, jsontext.EscapeForHTML(true), jsontext.EscapeForJS(true))
}

// WriteResponse writes the final success response for a completed login:
// a JSON body carrying the tokens directly for a non-browser client, or
// __Host-access_token/refresh_token/device_id cookies plus a minimal JSON
// body for a browser client.
func WriteResponse(w http.ResponseWriter, tokens *authtoken.Tokens, deviceID string, deviceIDIsFresh, nonBrowser bool) {
	w.Header().Set("Content-Type", "application/json")
	if nonBrowser {
		writeJSON(w, map[string]any{
			"access_token":  tokens.AccessToken,
			"refresh_token": tokens.RefreshToken,
			"device_id":     deviceID,
			"expires_in":    tokens.ExpiresIn,
		})
		return
	}

	setCookies(w, tokens, deviceID, deviceIDIsFresh)
	writeJSON(w, map[string]any{"expires_in": tokens.ExpiresIn})
}

// SetTokenCookies sets the __Host-access_token/refresh_token cookies
// every full-session response carries for a browser client — shared by
// setCookies (a fresh login) and authrefresh's rotation response, so the
// cookie flags (Path/MaxAge/Secure/SameSite) can't drift between the two
// call sites the way two independent copies risked.
func SetTokenCookies(w http.ResponseWriter, tokens *authtoken.Tokens) {
	http.SetCookie(w, &http.Cookie{
		Name:     "__Host-access_token",
		Value:    tokens.AccessToken,
		Path:     "/",
		MaxAge:   tokens.ExpiresIn,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
	})
	http.SetCookie(w, &http.Cookie{
		Name:     "refresh_token",
		Value:    tokens.RefreshToken,
		Path:     "/auth/refresh",
		MaxAge:   30 * 24 * 60 * 60,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
	})
}

func setCookies(w http.ResponseWriter, tokens *authtoken.Tokens, deviceID string, deviceIDIsFresh bool) {
	SetTokenCookies(w, tokens)
	if deviceIDIsFresh {
		http.SetCookie(w, &http.Cookie{
			Name:     "device_id",
			Value:    deviceID,
			Path:     "/auth/refresh",
			MaxAge:   30 * 24 * 60 * 60,
			HttpOnly: true,
			Secure:   true,
			SameSite: http.SameSiteStrictMode,
		})
	}
}
