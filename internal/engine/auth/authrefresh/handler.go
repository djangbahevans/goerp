// Package authrefresh implements POST /auth/refresh — auth-internals.md
// §4 "Refresh token rotation": exchange a still-live refresh token for a
// fresh access/refresh token pair inside session.Store.Rotate's own
// transactional rotate/replay-decision sequence. Not Class A in the usual
// JWT-or-API-key sense (auth-internals.md §9 "Route classes") — this
// route authenticates by the presented refresh token itself, not an
// access token, so it doesn't go through authcheck.Checker at all.
package authrefresh

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"net/http"
	"strings"

	"github.com/rs/zerolog/log"

	"github.com/djangbahevans/goerp/internal/engine/auth/authtoken"
	"github.com/djangbahevans/goerp/internal/engine/auth/loginsession"
	"github.com/djangbahevans/goerp/internal/engine/auth/session"
)

// maxBodyBytes bounds the request body before JSON parsing — no shared
// config field or middleware covers builtin routes yet, same reasoning
// loginflow/mfareverify's own caps use.
const maxBodyBytes = 64 * 1024

type Handler struct {
	issuer *authtoken.Issuer
}

func NewHandler(issuer *authtoken.Issuer) *Handler {
	return &Handler{issuer: issuer}
}

type refreshRequest struct {
	DeviceID string `json:"device_id"`
}

// writeJSON matches encoding/json v1's Encoder defaults, which
// json.MarshalWrite doesn't apply on its own: '<', '>', '&' escaped for
// safe HTML embedding, and U+2028/U+2029 escaped for safe JS embedding.
func writeJSON(w http.ResponseWriter, v any) {
	_ = json.MarshalWrite(w, v, jsontext.EscapeForHTML(true), jsontext.EscapeForJS(true))
}

func writeJSONError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	writeJSON(w, map[string]any{
		"error": map[string]string{"code": code, "message": message},
	})
}

func writeInvalidToken(w http.ResponseWriter) {
	writeJSONError(w, http.StatusUnauthorized, "invalid_refresh_token", "the refresh token is invalid, expired, or already used")
}

// extractRefreshToken returns the presented refresh token — the
// Authorization header if present (a non-browser client, which received
// its refresh token directly in the login response body and holds it
// itself), else the refresh_token cookie (a browser client — HttpOnly, so
// JS never handles the value itself, the browser just attaches it since
// this route's path matches the cookie's own Path=/auth/refresh scope).
// Mirrors authcheck.ExtractToken's precedence for the access token.
func extractRefreshToken(r *http.Request) string {
	if auth := r.Header.Get("Authorization"); auth != "" {
		return strings.TrimPrefix(auth, "Bearer ")
	}
	if cookie, err := r.Cookie("refresh_token"); err == nil {
		return cookie.Value
	}
	return ""
}

// extractDeviceID returns the request's own device_id if it presented
// one, else "" — never minted here (unlike loginsession.ResolveDeviceID,
// which mints a fresh one for a first-ever login). Rotate's own
// device_id-mismatch replay decision, and its "fall back to the old
// row's device_id" behavior for a rotating request that presented none,
// both depend on genuinely knowing the difference between "presented
// empty" and "presented this value."
func extractDeviceID(r *http.Request, bodyDeviceID string, nonBrowser bool) string {
	if nonBrowser {
		return bodyDeviceID
	}
	if cookie, err := r.Cookie("device_id"); err == nil {
		return cookie.Value
	}
	return ""
}

// writeTokens writes a successful rotation's new token pair: a JSON body
// for a non-browser client, or refreshed __Host-access_token/refresh_token
// cookies plus a minimal JSON body for a browser client. Unlike
// loginsession.WriteResponse, this never touches the device_id cookie —
// rotation doesn't mint a new device identity, only login does.
func writeTokens(w http.ResponseWriter, r *http.Request, tokens *authtoken.Tokens) {
	w.Header().Set("Content-Type", "application/json")

	if loginsession.IsNonBrowser(r) {
		writeJSON(w, map[string]any{
			"access_token":  tokens.AccessToken,
			"refresh_token": tokens.RefreshToken,
			"expires_in":    tokens.ExpiresIn,
		})
		return
	}

	loginsession.SetTokenCookies(w, tokens)
	writeJSON(w, map[string]any{"expires_in": tokens.ExpiresIn})
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	presentedToken := extractRefreshToken(r)
	if presentedToken == "" {
		writeInvalidToken(w)
		return
	}

	nonBrowser := loginsession.IsNonBrowser(r)
	var req refreshRequest
	if nonBrowser && r.ContentLength != 0 {
		r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
		if err := json.UnmarshalRead(r.Body, &req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid_request", "malformed request body")
			return
		}
	}
	deviceID := extractDeviceID(r, req.DeviceID, nonBrowser)

	tokens, outcome, err := h.issuer.Refresh(ctx, presentedToken, authtoken.RefreshParams{
		DeviceID:  deviceID,
		UserAgent: r.UserAgent(),
		IPAddress: loginsession.ClientIP(r),
	})
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal_error", "refresh failed")
		return
	}
	switch outcome {
	case session.RotateReplayDifferentDevice:
		log.Warn().Str("device_id", deviceID).Msg("authrefresh: replayed refresh token from a different device — session family revoked")
	case session.RotateReplaySameDevice:
		// auth-internals.md §4 step 5b: an info-level trail for diagnosing
		// double-submit patterns (network retry, duplicate tab) — not a
		// security event, so not a Warn like the cross-device case above.
		log.Info().Str("device_id", deviceID).Msg("authrefresh: replayed refresh token from the same device — likely a duplicate submission, not revoked")
	}
	if outcome != session.RotateOK {
		writeInvalidToken(w)
		return
	}

	writeTokens(w, r, tokens)
}
