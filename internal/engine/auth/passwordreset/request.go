package passwordreset

import (
	"context"
	"encoding/json/v2"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/djangbahevans/goerp/internal/engine/auth/loginsession"
	"github.com/djangbahevans/goerp/internal/engine/authaudit"
	"github.com/djangbahevans/goerp/internal/engine/cache"
	"github.com/djangbahevans/goerp/internal/engine/role"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	"github.com/djangbahevans/goerp/internal/engine/user"
)

const (
	requestsPerEmail = 3
	requestWindow    = time.Hour
	// minRequestResponseTime keeps an issued token and a skipped one
	// indistinguishable by latency (auth-internals.md §15).
	minRequestResponseTime = 300 * time.Millisecond
)

func rateLimitKey(email string) string {
	return "auth:password_reset:email:" + hashToken(email)
}

type RequestHandler struct {
	users   *user.Store
	tenants *tenant.Store
	roles   *role.Store
	cache   *cache.Client
	mailer  Mailer
	audit   AuditRecorder
}

func NewRequestHandler(users *user.Store, tenants *tenant.Store, roles *role.Store, cacheClient *cache.Client, mailer Mailer, audit AuditRecorder) *RequestHandler {
	return &RequestHandler{users: users, tenants: tenants, roles: roles, cache: cacheClient, mailer: mailer, audit: audit}
}

type resetRequest struct {
	Email  string `json:"email"`
	Tenant string `json:"tenant"`
}

// ServeHTTP answers 200 for every well-formed request, whether or not a
// token was issued, so the response never reveals whether an account
// exists.
func (h *RequestHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	defer func() {
		if elapsed := time.Since(start); elapsed < minRequestResponseTime {
			time.Sleep(minRequestResponseTime - elapsed)
		}
	}()

	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	var req resetRequest
	if err := json.UnmarshalRead(r.Body, &req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid_request", "malformed request body")
		return
	}

	if err := h.issue(r, req); err != nil {
		log.Error().Err(err).Msg("passwordreset: request failed")
	}

	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, map[string]any{"status": "ok"})
}

// errSkipped marks a request that deliberately issues no token.
var errSkipped = errors.New("skipped")

func (h *RequestHandler) issue(r *http.Request, req resetRequest) error {
	ctx := r.Context()
	email := strings.ToLower(req.Email)
	if email == "" {
		return nil
	}

	// Counted before the user lookup, so an unknown email is throttled
	// the same as a real one. A limiter failure issues nothing.
	count, err := h.cache.IncrWithTTL(ctx, rateLimitKey(email), requestWindow)
	if err != nil {
		return err
	}
	if count > requestsPerEmail {
		return nil
	}

	u, t, err := h.resolve(ctx, email, req.Tenant)
	if err != nil {
		if errors.Is(err, errSkipped) {
			return nil
		}
		return err
	}

	raw, hash, err := newToken()
	if err != nil {
		return err
	}
	if err := h.users.SetPasswordResetToken(ctx, u.ID, hash, time.Now().Add(tokenTTL)); err != nil {
		return err
	}

	recordAudit(ctx, h.audit, authaudit.Row{
		EventType: "password.reset_requested",
		TenantID:  t.ID,
		UserID:    u.ID,
		IPAddress: loginsession.ClientIP(r),
		UserAgent: r.UserAgent(),
		Success:   true,
	})

	if h.mailer == nil {
		log.Warn().Str("user_id", u.ID).Msg("passwordreset: no mailer wired, reset email not sent")
		return nil
	}
	sendDetached(ctx, "reset", func(ctx context.Context) error {
		return h.mailer.SendPasswordReset(ctx, u.Email, t.Slug, raw)
	})
	return nil
}

// resolve returns errSkipped unless the email belongs to a user who is a
// member of the named tenant. The link carries the tenant slug, so a
// token is never issued for a tenant the caller merely typed. GetBySlug
// runs before IsMember because IsMember interpolates the slug into a
// schema name, which is safe only for a slug read back from a real row.
func (h *RequestHandler) resolve(ctx context.Context, email, tenantSlug string) (*user.User, *tenant.Tenant, error) {
	u, err := h.users.GetByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, user.ErrUserNotFound) {
			return nil, nil, errSkipped
		}
		return nil, nil, err
	}

	t, err := h.tenants.GetBySlug(ctx, tenantSlug)
	if err != nil {
		if errors.Is(err, tenant.ErrTenantNotFound) {
			return nil, nil, errSkipped
		}
		return nil, nil, err
	}
	isMember, err := h.roles.IsMember(ctx, t.Slug, u.ID)
	if err != nil {
		return nil, nil, err
	}
	if !isMember {
		return nil, nil, errSkipped
	}

	return u, t, nil
}
