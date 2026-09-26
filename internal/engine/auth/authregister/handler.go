// Package authregister implements POST /auth/register and GET
// /auth/check-slug — auth-internals.md §3 "Self-service registration" and
// "Email verification policy", shell-ux.md §2.2. Both answer 404 unless
// GOERP_REGISTRATION_ENABLED is on. Registration always founds a new
// tenant: the account is created with its password already set, then
// ProvisionTenantWorkflow runs to completion with that account as the
// founding admin before the response is written.
package authregister

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"net/http"
	"net/mail"
	"strconv"
	"strings"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/djangbahevans/goerp/internal/engine/auth/authtoken"
	"github.com/djangbahevans/goerp/internal/engine/auth/emailverify"
	"github.com/djangbahevans/goerp/internal/engine/auth/handoff"
	"github.com/djangbahevans/goerp/internal/engine/auth/loginsession"
	"github.com/djangbahevans/goerp/internal/engine/auth/password"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	tenantprovision "github.com/djangbahevans/goerp/internal/engine/tenant/provision"
	"github.com/djangbahevans/goerp/internal/engine/user"
)

const (
	maxBodyBytes = 64 * 1024
)

// Verification policies — the values of GOERP_REQUIRE_EMAIL_VERIFICATION.
const (
	VerificationRequired     = "required"
	VerificationTenantChoice = "tenant_choice"
	VerificationOff          = "off"
)

// Provisioner is satisfied by tenantprovision.Provisioner.
type Provisioner interface {
	ProvisionForRegistration(ctx context.Context, slug, name, userID string) error
}

type Config struct {
	Enabled bool
	// VerificationPolicy is GOERP_REQUIRE_EMAIL_VERIFICATION.
	VerificationPolicy string
	// ProvisionTimeout bounds the wait for the tenant; it must leave room
	// under the server's write timeout to send the response.
	ProvisionTimeout time.Duration
}

type Handlers struct {
	cfg         Config
	users       *user.Store
	tenants     *tenant.Store
	provisioner Provisioner
	hasher      *password.Hasher
	issuer      *authtoken.Issuer
	mailer      emailverify.Mailer
	handoffs    *handoff.Store
}

func NewHandlers(cfg Config, users *user.Store, tenants *tenant.Store, provisioner Provisioner, hasher *password.Hasher, issuer *authtoken.Issuer, mailer emailverify.Mailer, handoffs *handoff.Store) *Handlers {
	return &Handlers{cfg: cfg, users: users, tenants: tenants, provisioner: provisioner, hasher: hasher, issuer: issuer, mailer: mailer, handoffs: handoffs}
}

// requiresVerification is auth-internals.md §3's registration-time
// resolution: the tenant this request founds has no config yet, so
// tenant_choice resolves to the same fixed default as required.
func requiresVerification(policy string) bool {
	return policy != VerificationOff
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.MarshalWrite(w, v, jsontext.EscapeForHTML(true), jsontext.EscapeForJS(true))
}

func writeJSONError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]string{"code": code, "message": message},
	})
}

func writeNotFound(w http.ResponseWriter) {
	writeJSONError(w, http.StatusNotFound, "not_found", "not found")
}

func writeInternal(w http.ResponseWriter) {
	writeJSONError(w, http.StatusInternalServerError, "internal_error", "registration failed")
}

// CheckSlug serves GET /auth/check-slug?slug= — the register page's
// availability hint while the company name is typed.
func (h *Handlers) CheckSlug(w http.ResponseWriter, r *http.Request) {
	if !h.cfg.Enabled {
		writeNotFound(w)
		return
	}
	slug := r.URL.Query().Get("slug")
	available, err := h.slugAvailable(r.Context(), slug)
	if err != nil {
		writeInternal(w)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"available": available})
}

// slugAvailable reports whether slug is well-formed, not reserved, and
// held by no tenant row in any status (the slug column is unique across
// all of them). A reserved slug reads as taken, so the register page's
// "try another name" handling covers it.
func (h *Handlers) slugAvailable(ctx context.Context, slug string) (bool, error) {
	if !validSlug(slug) || h.tenants.IsReserved(slug) {
		return false, nil
	}
	_, err := h.tenants.GetBySlug(ctx, slug)
	if errors.Is(err, tenant.ErrTenantNotFound) {
		return true, nil
	}
	return false, err
}

type registerRequest struct {
	Name        string `json:"name"`
	Email       string `json:"email"`
	Password    string `json:"password"`
	CompanyName string `json:"company_name"`
	DeviceID    string `json:"device_id"`
}

// validate returns per-field messages for the 422 body, and the derived
// slug.
func validate(req registerRequest) (map[string]string, string) {
	problems := map[string]string{}
	if strings.TrimSpace(req.Name) == "" {
		problems["name"] = "Enter your name."
	}
	if addr, err := mail.ParseAddress(req.Email); err != nil || addr.Address != req.Email {
		problems["email"] = "Enter a valid email address."
	}
	slug := DeriveSlug(req.CompanyName)
	if strings.TrimSpace(req.CompanyName) == "" {
		problems["company_name"] = "Enter your company name."
	} else if !validSlug(slug) {
		problems["company_name"] = "Use at least 3 letters or digits in your company name."
	}
	if err := password.Global.Validate(req.Password, req.Email); err != nil {
		problems["password"] = err.Error()
	}
	return problems, slug
}

// Register serves POST /auth/register.
func (h *Handlers) Register(w http.ResponseWriter, r *http.Request) {
	if !h.cfg.Enabled {
		writeNotFound(w)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	var req registerRequest
	if err := json.UnmarshalRead(r.Body, &req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid_request", "malformed request body")
		return
	}
	req.Email = strings.ToLower(strings.TrimSpace(req.Email))
	req.Name = strings.TrimSpace(req.Name)
	req.CompanyName = strings.TrimSpace(req.CompanyName)

	problems, slug := validate(req)
	if len(problems) > 0 {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
			"error": map[string]any{"code": "validation_failed", "message": "some fields are invalid", "details": problems},
		})
		return
	}

	ctx := r.Context()
	if _, err := h.users.GetByEmail(ctx, req.Email); err == nil {
		writeJSONError(w, http.StatusConflict, "auth.email_already_exists", "email already in use")
		return
	} else if !errors.Is(err, user.ErrUserNotFound) {
		writeInternal(w)
		return
	}
	available, err := h.slugAvailable(ctx, slug)
	if err != nil {
		writeInternal(w)
		return
	}
	if !available {
		writeJSONError(w, http.StatusConflict, "tenant.slug_taken", "company name taken, try another")
		return
	}

	slot, err := h.hasher.Acquire(ctx)
	if err != nil {
		w.Header().Set("Retry-After", strconv.Itoa(password.OverloadRetryAfterSeconds))
		writeJSONError(w, http.StatusServiceUnavailable, "overloaded", "too many password operations in progress, retry shortly")
		return
	}
	hash, err := slot.Hash(req.Password)
	slot.Release()
	if err != nil {
		writeInternal(w)
		return
	}

	verify := requiresVerification(h.cfg.VerificationPolicy)
	status := user.StatusActive
	if verify {
		status = user.StatusPendingVerification
	}
	userID, err := h.users.CreateRegistered(ctx, req.Email, hash, status)
	if err != nil {
		if errors.Is(err, user.ErrEmailTaken) {
			writeJSONError(w, http.StatusConflict, "auth.email_already_exists", "email already in use")
			return
		}
		writeInternal(w)
		return
	}
	if err := h.users.EnsureProfile(ctx, userID, req.Name); err != nil {
		h.abandon(ctx, userID)
		writeInternal(w)
		return
	}

	// Detached from the client: a disconnect mustn't end the wait early,
	// since the workflow keeps running either way.
	provisionCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), h.cfg.ProvisionTimeout)
	err = h.provisioner.ProvisionForRegistration(provisionCtx, slug, req.CompanyName, userID)
	cancel()
	if errors.Is(err, tenantprovision.ErrProvisioningPending) {
		// Still running, and it will grant this account admin when it
		// finishes, so the account stays.
		log.Warn().Str("slug", slug).Msg("authregister: provisioning outlasted the request")
		if verify {
			if err := h.sendVerification(ctx, userID, req.Email, slug); err != nil {
				log.Error().Err(err).Str("user_id", userID).Msg("authregister: storing verification token failed")
			}
		}
		writeJSON(w, http.StatusAccepted, map[string]any{"requires_email_verification": verify, "provisioning_pending": true, "tenant_slug": slug})
		return
	}
	if err != nil {
		h.abandon(ctx, userID)
		if errors.Is(err, tenantprovision.ErrSlugTaken) {
			writeJSONError(w, http.StatusConflict, "tenant.slug_taken", "company name taken, try another")
			return
		}
		log.Error().Err(err).Str("slug", slug).Msg("authregister: provisioning failed")
		writeInternal(w)
		return
	}

	if verify {
		if err := h.sendVerification(ctx, userID, req.Email, slug); err != nil {
			log.Error().Err(err).Str("user_id", userID).Msg("authregister: storing verification token failed")
		}
		// tenant_slug lets the page offer "Resend email" (auth-internals.md
		// §3 "Resend email verification").
		writeJSON(w, http.StatusAccepted, map[string]any{"requires_email_verification": true, "tenant_slug": slug})
		return
	}

	// Registration normally runs on the shared-domain host, which no
	// tenant owns, so a browser is signed in on the new tenant's own host
	// (auth-internals.md §3 "Shared-domain handoff").
	t, err := h.tenants.GetBySlug(ctx, slug)
	if err != nil {
		log.Error().Err(err).Str("slug", slug).Msg("authregister: looking up the new tenant failed")
		writeJSON(w, http.StatusCreated, map[string]any{"tenant_slug": slug, "login_required": true})
		return
	}
	if h.handoffs.Needed(ctx, r, t.ID) {
		resp, err := h.handoffs.Issue(ctx, handoff.Grant{UserID: userID, TenantID: t.ID}, slug)
		if err != nil {
			log.Error().Err(err).Str("user_id", userID).Msg("authregister: issuing handoff failed")
			writeJSON(w, http.StatusCreated, map[string]any{"tenant_slug": slug, "login_required": true})
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{"tenant_slug": slug, "handoff": resp})
		return
	}

	nonBrowser := loginsession.IsNonBrowser(r)
	deviceID, deviceIDIsFresh := loginsession.ResolveDeviceID(r, req.DeviceID, nonBrowser)
	tokens, err := h.issuer.Issue(ctx, authtoken.LoginParams{
		UserID:     userID,
		TenantSlug: slug,
		DeviceID:   deviceID,
		UserAgent:  r.UserAgent(),
		IPAddress:  loginsession.ClientIP(r),
		Persistent: nonBrowser,
	})
	if err != nil {
		// The account and tenant exist; only the automatic sign-in failed.
		log.Error().Err(err).Str("user_id", userID).Msg("authregister: issuing tokens failed")
		writeJSON(w, http.StatusCreated, map[string]any{"tenant_slug": slug, "login_required": true})
		return
	}
	loginsession.WriteRegisteredResponse(w, tokens, deviceID, deviceIDIsFresh, nonBrowser, slug)
}

// abandon removes the account a failed registration created, so the email
// can register again. A tenant left behind by a half-finished workflow
// stays in status provisioning, holding its slug.
func (h *Handlers) abandon(ctx context.Context, userID string) {
	if err := h.users.DeleteRegistered(context.WithoutCancel(ctx), userID); err != nil {
		log.Error().Err(err).Str("user_id", userID).Msg("authregister: removing abandoned account failed")
	}
}

// sendVerification stores the hashed verification token and emails the
// link off the request goroutine.
func (h *Handlers) sendVerification(ctx context.Context, userID, email, tenantSlug string) error {
	raw, err := emailverify.IssueToken(ctx, h.users, userID)
	if err != nil {
		return err
	}
	emailverify.SendDetached(ctx, h.mailer, email, tenantSlug, raw)
	return nil
}
