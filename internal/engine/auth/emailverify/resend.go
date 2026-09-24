package emailverify

import (
	"context"
	"encoding/json/v2"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/djangbahevans/goerp/internal/engine/cache"
	"github.com/djangbahevans/goerp/internal/engine/role"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	"github.com/djangbahevans/goerp/internal/engine/user"
)

const (
	resendsPerEmail = 3
	resendWindow    = time.Hour
	// minResendResponseTime keeps a sent link and a skipped one
	// indistinguishable by latency (auth-internals.md §15).
	minResendResponseTime = 300 * time.Millisecond
)

func rateLimitKey(email string) string {
	return "auth:verify_email_resend:email:" + hashToken(email)
}

type ResendHandler struct {
	users   *user.Store
	tenants *tenant.Store
	roles   *role.Store
	cache   *cache.Client
	mailer  Mailer
}

func NewResendHandler(users *user.Store, tenants *tenant.Store, roles *role.Store, cacheClient *cache.Client, mailer Mailer) *ResendHandler {
	return &ResendHandler{users: users, tenants: tenants, roles: roles, cache: cacheClient, mailer: mailer}
}

type resendRequest struct {
	Email  string `json:"email"`
	Tenant string `json:"tenant"`
}

// ServeHTTP answers 200 for every well-formed request, whether or not a
// link was sent, so the response never reveals whether an account exists.
func (h *ResendHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	defer func() {
		if elapsed := time.Since(start); elapsed < minResendResponseTime {
			time.Sleep(minResendResponseTime - elapsed)
		}
	}()

	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	var req resendRequest
	if err := json.UnmarshalRead(r.Body, &req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid_request", "malformed request body")
		return
	}

	if err := h.resend(r.Context(), req); err != nil {
		log.Error().Err(err).Msg("emailverify: resend failed")
	}

	writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}

func (h *ResendHandler) resend(ctx context.Context, req resendRequest) error {
	email := strings.ToLower(strings.TrimSpace(req.Email))
	if email == "" {
		return nil
	}

	// Counted before the user lookup, so an unknown email is throttled
	// the same as a real one. A limiter failure sends nothing.
	count, err := h.cache.IncrWithTTL(ctx, rateLimitKey(email), resendWindow)
	if err != nil {
		return err
	}
	if count > resendsPerEmail {
		return nil
	}

	u, t, ok, err := h.resolve(ctx, email, req.Tenant)
	if err != nil || !ok {
		return err
	}

	raw, err := IssueToken(ctx, h.users, u.ID)
	if err != nil {
		return err
	}
	SendDetached(ctx, h.mailer, u.Email, t.Slug, raw)
	return nil
}

// resolve reports ok only for a pending_verification account that is a
// member of the named tenant. GetBySlug runs before IsMember because
// IsMember interpolates the slug into a schema name, which is safe only
// for a slug read back from a real row.
func (h *ResendHandler) resolve(ctx context.Context, email, tenantSlug string) (*user.User, *tenant.Tenant, bool, error) {
	u, err := h.users.GetByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, user.ErrUserNotFound) {
			return nil, nil, false, nil
		}
		return nil, nil, false, err
	}
	if u.Status != user.StatusPendingVerification {
		return nil, nil, false, nil
	}

	t, err := h.tenants.GetBySlug(ctx, tenantSlug)
	if err != nil {
		if errors.Is(err, tenant.ErrTenantNotFound) {
			return nil, nil, false, nil
		}
		return nil, nil, false, err
	}
	isMember, err := h.roles.IsMember(ctx, t.Slug, u.ID)
	if err != nil || !isMember {
		return nil, nil, false, err
	}

	return u, t, true, nil
}
