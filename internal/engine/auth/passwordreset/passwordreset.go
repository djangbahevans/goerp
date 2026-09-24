// Package passwordreset implements POST /auth/password-reset/request and
// POST /auth/password-reset/confirm — auth-internals.md §3 "Password
// reset". Both are Class B routes (§9): the tenant comes from the request
// body, and the reset link carries it as ?tenant= for the confirm page to
// send back.
//
// Out of scope, left to the tickets that own them: the global Argon2
// concurrency semaphore (backlog #286) and the per-tenant PasswordPolicy
// with its common-password blocklist (backlog #251).
package passwordreset

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

	"github.com/djangbahevans/goerp/internal/engine/authaudit"
)

const (
	maxBodyBytes = 64 * 1024
	tokenBytes   = 32
	tokenTTL     = time.Hour
	// mailTimeout bounds the detached email send, which outlives the request.
	mailTimeout = 30 * time.Second
)

type Mailer interface {
	SendPasswordReset(ctx context.Context, email, tenantSlug, rawToken string) error
	SendPasswordResetConfirmed(ctx context.Context, email string) error
}

// AuditRecorder is satisfied by authaudit.Store.
type AuditRecorder interface {
	Insert(ctx context.Context, row authaudit.Row) error
}

func newToken() (raw, hash string, err error) {
	buf := make([]byte, tokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", "", err
	}
	raw = base64.RawURLEncoding.EncodeToString(buf)
	return raw, hashToken(raw), nil
}

func hashToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// writeJSON matches encoding/json v1's Encoder defaults, which
// json.MarshalWrite doesn't apply on its own.
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

func recordAudit(ctx context.Context, audit AuditRecorder, row authaudit.Row) {
	if audit == nil {
		log.Warn().Str("event", row.EventType).Msg("passwordreset: no audit recorder wired, event not recorded")
		return
	}
	if err := audit.Insert(ctx, row); err != nil {
		log.Warn().Err(err).Str("event", row.EventType).Msg("passwordreset: audit insert failed")
	}
}

// sendDetached runs send off the request goroutine, so SMTP latency can't
// reveal to the caller whether an email was sent at all.
func sendDetached(ctx context.Context, what string, send func(context.Context) error) {
	ctx = context.WithoutCancel(ctx)
	go func() {
		ctx, cancel := context.WithTimeout(ctx, mailTimeout)
		defer cancel()
		if err := send(ctx); err != nil {
			log.Warn().Err(err).Msgf("passwordreset: %s email failed", what)
		}
	}()
}
