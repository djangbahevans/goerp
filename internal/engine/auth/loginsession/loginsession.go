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
// body for a browser client. passwordUpdateRecommended adds
// auth-internals.md §3's "password_update_recommended" nudge.
func WriteResponse(w http.ResponseWriter, tokens *authtoken.Tokens, deviceID string, deviceIDIsFresh, nonBrowser, passwordUpdateRecommended bool) {
	body := map[string]any{}
	if passwordUpdateRecommended {
		body["password_update_recommended"] = true
	}
	write(w, http.StatusOK, tokens, deviceID, deviceIDIsFresh, nonBrowser, body)
}

// WriteRegisteredResponse is WriteResponse for a self-registration that
// signs its user straight in: 201 Created, with the new tenant's slug.
func WriteRegisteredResponse(w http.ResponseWriter, tokens *authtoken.Tokens, deviceID string, deviceIDIsFresh, nonBrowser bool, tenantSlug string) {
	write(w, http.StatusCreated, tokens, deviceID, deviceIDIsFresh, nonBrowser, map[string]any{"tenant_slug": tenantSlug})
}

func write(w http.ResponseWriter, status int, tokens *authtoken.Tokens, deviceID string, deviceIDIsFresh, nonBrowser bool, body map[string]any) {
	body["expires_in"] = tokens.ExpiresIn
	if nonBrowser {
		body["access_token"] = tokens.AccessToken
		body["refresh_token"] = tokens.RefreshToken
		body["device_id"] = deviceID
	} else {
		setCookies(w, tokens, deviceID, deviceIDIsFresh)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	writeJSON(w, body)
}

// SetTokenCookies sets the __Host-access_token/refresh_token cookies
// every full-session response carries for a browser client — shared by
// setCookies (a fresh login) and authrefresh's rotation response, so the
// cookie flags (Path/MaxAge/Secure/SameSite) can't drift between the two
// call sites the way two independent copies risked. A non-persistent
// session's cookies are browser-session cookies (no Max-Age), so they end
// when the browser closes; the access token's own exp still bounds it.
func SetTokenCookies(w http.ResponseWriter, tokens *authtoken.Tokens) {
	accessMaxAge, refreshMaxAge := 0, 0
	if tokens.Persistent {
		accessMaxAge = tokens.ExpiresIn
		refreshMaxAge = int(authtoken.PersistentRefreshTTL.Seconds())
	}
	http.SetCookie(w, &http.Cookie{
		Name:     "__Host-access_token",
		Value:    tokens.AccessToken,
		Path:     "/",
		MaxAge:   accessMaxAge,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
	})
	http.SetCookie(w, &http.Cookie{
		Name:     "refresh_token",
		Value:    tokens.RefreshToken,
		Path:     "/auth/refresh",
		MaxAge:   refreshMaxAge,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
	})
}

// ClearCookies expires the two session cookies SetTokenCookies sets at
// login — __Host-access_token and refresh_token — for a browser client's
// logout. device_id is deliberately left alone: it identifies the
// physical device across logins (30-day lifetime, reused by
// ResolveDeviceID on the next login), not the session being ended here —
// clearing it on every logout would make a returning device look
// unrecognized on its very next sign-in. Path/Name/Secure/SameSite must
// match SetTokenCookies' exactly, or the browser treats this as a
// different cookie and leaves the original one in place instead of
// clearing it.
func ClearCookies(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     "__Host-access_token",
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
	})
	http.SetCookie(w, &http.Cookie{
		Name:     "refresh_token",
		Value:    "",
		Path:     "/auth/refresh",
		MaxAge:   -1,
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
