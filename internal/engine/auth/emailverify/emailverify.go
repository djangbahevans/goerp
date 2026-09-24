// Package emailverify implements POST /auth/verify-email and POST
// /auth/verify-email/resend — auth-internals.md §3 "Email verification
// confirm" and "Resend email verification" — and issues the verification
// token self-service registration emails. Both routes are Class B (§9):
// the tenant comes from the request body, which the verification link
// carries as ?tenant=.
package emailverify

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"net/http"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/djangbahevans/goerp/internal/engine/user"
)

const (
	maxBodyBytes = 64 * 1024
	tokenBytes   = 32
	tokenTTL     = 24 * time.Hour
	// mailTimeout bounds the detached email send, which outlives the request.
	mailTimeout = 30 * time.Second
)

type Mailer interface {
	SendVerifyEmail(ctx context.Context, email, tenantSlug, rawToken string) error
}

// IssueToken stores a fresh 24-hour verification token for userID,
// replacing any earlier one, and returns the raw token for the emailed
// link.
func IssueToken(ctx context.Context, users *user.Store, userID string) (string, error) {
	buf := make([]byte, tokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	raw := base64.RawURLEncoding.EncodeToString(buf)
	if err := users.SetEmailVerifyToken(ctx, userID, hashToken(raw), time.Now().Add(tokenTTL)); err != nil {
		return "", err
	}
	return raw, nil
}

// SendDetached emails the verification link off the request goroutine, so
// SMTP latency never shows in the response.
func SendDetached(ctx context.Context, mailer Mailer, email, tenantSlug, rawToken string) {
	if mailer == nil {
		log.Warn().Str("tenant", tenantSlug).Msg("emailverify: no mailer wired, verification email not sent")
		return
	}
	ctx = context.WithoutCancel(ctx)
	go func() {
		ctx, cancel := context.WithTimeout(ctx, mailTimeout)
		defer cancel()
		if err := mailer.SendVerifyEmail(ctx, email, tenantSlug, rawToken); err != nil {
			log.Warn().Err(err).Str("tenant", tenantSlug).Msg("emailverify: verification email failed")
		}
	}()
}

func hashToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	// The options match encoding/json v1's Encoder defaults, which
	// json.MarshalWrite doesn't apply on its own.
	_ = json.MarshalWrite(w, v, jsontext.EscapeForHTML(true), jsontext.EscapeForJS(true))
}

func writeJSONError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]string{"code": code, "message": message},
	})
}
