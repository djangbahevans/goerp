// Package passwordreset implements password-reset request and confirmation routes,
// resolving the tenant from the request body.
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
