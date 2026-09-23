// Package tenantcontext implements GET /auth/tenant-context — the
// anonymous lookup the shell's login page runs before any session exists,
// to learn which tenant the current Host resolves to (shell-ux.md §2.1
// "Tenant resolution"). A Host that resolves to no tenant is a
// shared-domain deployment: the response carries a null tenant and the
// login page asks for the company slug instead.
package tenantcontext

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"net/http"

	tenantresolve "github.com/djangbahevans/goerp/internal/engine/tenant/resolve"
)

type Handler struct {
	tenants *tenantresolve.Resolver
}

func NewHandler(tenants *tenantresolve.Resolver) *Handler {
	return &Handler{tenants: tenants}
}

type tenantSummary struct {
	Slug string `json:"slug"`
	Name string `json:"name"`
}

// RegistrationEnabled is always false: self-service registration
// (POST /auth/register) is not implemented, so no platform setting backs
// it yet.
type response struct {
	Tenant              *tenantSummary `json:"tenant"`
	RegistrationEnabled bool           `json:"registration_enabled"`
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
		writeJSON(w, http.StatusOK, response{Tenant: &tenantSummary{Slug: tenantCtx.Slug, Name: tenantCtx.Name}})
	case errors.Is(err, tenantresolve.ErrTenantNotFound):
		writeJSON(w, http.StatusOK, response{})
	case errors.Is(err, tenantresolve.ErrTenantSuspended):
		writeJSONError(w, http.StatusForbidden, "tenant_suspended", "tenant suspended")
	case errors.Is(err, tenantresolve.ErrTenantOffboarding):
		writeJSONError(w, http.StatusForbidden, "tenant_offboarding", "tenant offboarding")
	default:
		writeJSONError(w, http.StatusInternalServerError, "internal_error", "tenant lookup failed")
	}
}
