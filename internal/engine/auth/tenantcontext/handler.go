// Package tenantcontext implements GET /auth/tenant-context — the
// anonymous lookup the shell's login page runs before any session exists,
// to learn which tenant the current Host resolves to (shell-ux.md §2.1
// "Tenant resolution"). The shared-domain host (GOERP_APP_BASE_URL's)
// answers with a null tenant; any other Host that resolves to no tenant is
// a wrong workspace address and answers 404 tenant_not_found.
package tenantcontext

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"

	tenantresolve "github.com/djangbahevans/goerp/internal/engine/tenant/resolve"
)

type Handler struct {
	tenants             *tenantresolve.Resolver
	registrationEnabled bool
	termsURL            *string
	appBaseURL          string
	sharedHost          string
}

type Config struct {
	RegistrationEnabled bool
	// TermsURL is "" when no terms of service are configured.
	TermsURL string
	// AppBaseURL is GOERP_APP_BASE_URL; its host is the shared domain.
	AppBaseURL string
}

func NewHandler(tenants *tenantresolve.Resolver, cfg Config) *Handler {
	h := &Handler{tenants: tenants, registrationEnabled: cfg.RegistrationEnabled, appBaseURL: cfg.AppBaseURL}
	if cfg.TermsURL != "" {
		h.termsURL = &cfg.TermsURL
	}
	if u, err := url.Parse(cfg.AppBaseURL); err == nil {
		h.sharedHost = strings.ToLower(u.Hostname())
	}
	return h
}

func (h *Handler) isSharedHost(host string) bool {
	if hostname, _, err := net.SplitHostPort(host); err == nil {
		host = hostname
	}
	return h.sharedHost != "" && strings.EqualFold(host, h.sharedHost)
}

type tenantSummary struct {
	Slug string `json:"slug"`
	Name string `json:"name"`
}

// RegistrationEnabled is the platform's GOERP_REGISTRATION_ENABLED: the
// login page's "Create an account" link.
type response struct {
	Tenant              *tenantSummary `json:"tenant"`
	RegistrationEnabled bool           `json:"registration_enabled"`
	TermsURL            *string        `json:"terms_url"`
	AppURL              string         `json:"app_url"`
}

// writeJSON matches encoding/json v1's Encoder defaults, which
// json.MarshalWrite doesn't apply on its own: '<', '>', '&' escaped for
// safe HTML embedding, and U+2028/U+2029 escaped for safe JS embedding.
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

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	tenantCtx, err := h.tenants.ResolveByHost(r.Context(), r.Host)
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, response{Tenant: &tenantSummary{Slug: tenantCtx.Slug, Name: tenantCtx.Name}, RegistrationEnabled: h.registrationEnabled, TermsURL: h.termsURL, AppURL: h.appBaseURL})
	case errors.Is(err, tenantresolve.ErrTenantNotFound) && h.isSharedHost(r.Host):
		writeJSON(w, http.StatusOK, response{RegistrationEnabled: h.registrationEnabled, TermsURL: h.termsURL, AppURL: h.appBaseURL})
	case errors.Is(err, tenantresolve.ErrTenantNotFound):
		writeJSON(w, http.StatusNotFound, map[string]any{
			"error": map[string]any{"code": "tenant_not_found", "message": "no workspace at this address", "details": map[string]string{"app_url": h.appBaseURL}},
		})
	case errors.Is(err, tenantresolve.ErrTenantSuspended):
		writeJSONError(w, http.StatusForbidden, "tenant_suspended", "tenant suspended")
	case errors.Is(err, tenantresolve.ErrTenantOffboarding):
		writeJSONError(w, http.StatusForbidden, "tenant_offboarding", "tenant offboarding")
	default:
		writeJSONError(w, http.StatusInternalServerError, "internal_error", "tenant lookup failed")
	}
}
