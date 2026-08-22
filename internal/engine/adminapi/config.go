package adminapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/djangbahevans/goerp/internal/engine/tenant"
)

type ConfigDeps struct {
	Tenants TenantResolver
	Config  ConfigStore
}

// TenantResolver is satisfied by *tenant.Store (GetBySlug) — resolving
// the URL's {slug} to the tenant_id ConfigStore keys on.
type TenantResolver interface {
	GetBySlug(ctx context.Context, slug string) (*tenant.Tenant, error)
}

// ConfigStore is satisfied by *tenantconfig.Store (Set).
type ConfigStore interface {
	Set(ctx context.Context, tenantID, key, value string) error
}

func RegisterConfigRoutes(mux *http.ServeMux, deps ConfigDeps) {
	h := &configHandlers{deps: deps}
	mux.HandleFunc("PATCH /admin/tenants/{slug}/config", h.set)
}

type configHandlers struct {
	deps ConfigDeps
}

type setConfigRequest struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

func (h *configHandlers) resolveTenantID(w http.ResponseWriter, r *http.Request) (string, bool) {
	slug := r.PathValue("slug")
	t, err := h.deps.Tenants.GetBySlug(r.Context(), slug)
	if err != nil {
		if errors.Is(err, tenant.ErrTenantNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "tenant not found")
			return "", false
		}
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return "", false
	}
	return t.ID, true
}

func (h *configHandlers) set(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := h.resolveTenantID(w, r)
	if !ok {
		return
	}

	req, err := decodeJSON[setConfigRequest](r)
	if err != nil || req.Key == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "key is required")
		return
	}

	if err := h.deps.Config.Set(r.Context(), tenantID, req.Key, req.Value); err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}

	writeData(w, http.StatusOK, req)
}
