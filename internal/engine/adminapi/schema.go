// Package adminapi's schema routes (goerp#292) wrap the already-built
// internal/engine/schema diff/classify/apply pipeline and
// internal/engine/tenant/sync's per-(tenant, module) sync logic — status
// and diff are synchronous reads, sync and accept are async (§11a),
// enqueuing a River job via SchemaSyncer/SchemaAccepter the same way
// tenant.go's export/import routes do.
package adminapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/schema"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	tenantsync "github.com/djangbahevans/goerp/internal/engine/tenant/sync"
)

type SchemaDeps struct {
	Status SchemaStatusReader
	Diff   SchemaDiffer
	Sync   SchemaSyncer
	Accept SchemaAccepter
}

// SchemaStatusReader is satisfied by *tenantsync.Admin (Status) — every
// module_schema_versions row across every tenant, optionally filtered.
type SchemaStatusReader interface {
	Status(ctx context.Context, tenantSlug, moduleName, filter string) ([]schema.TenantModuleStatus, error)
}

// SchemaDiffer is satisfied by *tenantsync.Admin (Diff) — the pending
// safe/deferred/blocked change set for one (tenant, module) pair, computed
// fresh, never applied.
type SchemaDiffer interface {
	Diff(ctx context.Context, tenantSlug, moduleName string, verbose bool) (version string, safe, deferred, blocked []schema.ChangeSummary, err error)
}

// SchemaSyncer is satisfied by *tenantsync.Admin (StartSync).
type SchemaSyncer interface {
	StartSync(ctx context.Context, tenantSlug, moduleName string, scheduledAt *time.Time) (jobID string, err error)
}

// SchemaAccepter is satisfied by *tenantsync.Admin (Accept).
type SchemaAccepter interface {
	Accept(ctx context.Context, tenantSlug, moduleName, reason, operator string) (acceptanceIDs []string, jobID string, err error)
}

func RegisterSchemaRoutes(mux *http.ServeMux, deps SchemaDeps) {
	h := &schemaHandlers{deps: deps}
	mux.HandleFunc("GET /admin/schema/status", h.status)
	mux.HandleFunc("GET /admin/modules/{name}/schema", h.diff)
	mux.HandleFunc("POST /admin/schema/sync", h.sync)
	mux.HandleFunc("POST /admin/schema/accept", h.accept)
}

type schemaHandlers struct {
	deps SchemaDeps
}

// writeSchemaResolveError maps the two resolve-time errors every schema
// route can hit (an unknown tenant slug, an unloaded module name) to a
// 404 — matching tenant.go/activitydispatch.go's own convention for the
// identical case — rather than falling through to a generic 500 for what
// is really a routine client input error. Anything else is a genuine
// internal failure.
func writeSchemaResolveError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, tenant.ErrTenantNotFound):
		writeError(w, http.StatusNotFound, "not_found", "tenant not found")
	case errors.Is(err, tenantsync.ErrModuleNotLoaded):
		writeError(w, http.StatusNotFound, "module_not_found", err.Error())
	default:
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
	}
}

// validSchemaStatusFilters is cli-reference.md §4's documented `schema
// status --filter` value set — "" (no filter) plus every literal
// StatusFiltered matches directly ("ok"/"failed"/"in_progress") and the
// one tenantsync.Admin.Status computes itself ("pending").
var validSchemaStatusFilters = map[string]bool{
	"":            true,
	"ok":          true,
	"failed":      true,
	"in_progress": true,
	"pending":     true,
}

func (h *schemaHandlers) status(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	filter := q.Get("filter")
	if !validSchemaStatusFilters[filter] {
		writeError(w, http.StatusBadRequest, "invalid_request", fmt.Sprintf("filter must be one of ok, failed, in_progress, pending (got %q)", filter))
		return
	}

	statuses, err := h.deps.Status.Status(r.Context(), q.Get("tenant"), q.Get("module"), filter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	writeData(w, http.StatusOK, statuses)
}

// schemaDiffResponse is GET /admin/modules/{name}/schema's response body —
// adminapi's own shape, built from SchemaDiffer's returned pieces rather
// than a type from the package implementing that interface, so this
// package's response shape doesn't change if that implementation's own
// internal types do.
type schemaDiffResponse struct {
	Module   string                 `json:"module"`
	Tenant   string                 `json:"tenant"`
	Version  string                 `json:"version"`
	Safe     []schema.ChangeSummary `json:"safe"`
	Deferred []schema.ChangeSummary `json:"deferred"`
	Blocked  []schema.ChangeSummary `json:"blocked"`
}

func (h *schemaHandlers) diff(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	tenantSlug := r.URL.Query().Get("tenant")
	if tenantSlug == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "tenant is required")
		return
	}

	verbose := r.URL.Query().Get("verbose") == "true"
	version, safe, deferred, blocked, err := h.deps.Diff.Diff(r.Context(), tenantSlug, name, verbose)
	if err != nil {
		writeSchemaResolveError(w, err)
		return
	}

	writeData(w, http.StatusOK, schemaDiffResponse{
		Module:   name,
		Tenant:   tenantSlug,
		Version:  version,
		Safe:     safe,
		Deferred: deferred,
		Blocked:  blocked,
	})
}

type schemaSyncRequest struct {
	Tenant   string `json:"tenant"`
	Module   string `json:"module"`
	Schedule string `json:"schedule"`
}

// parseSchedule requires RFC 3339 with an explicit offset or "Z" —
// cli-reference.md §4: "a bare, zone-less timestamp is rejected, not
// interpreted as local time." time.RFC3339 itself already requires a
// zone designator, so a bare Parse failure here already covers that case;
// this just gives it a clearer error message than time.Parse's own.
func parseSchedule(s string) (*time.Time, error) {
	if s == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return nil, fmt.Errorf("schedule must be RFC 3339 with an explicit offset or \"Z\" (e.g. \"2026-05-09T02:00:00Z\"): %w", err)
	}
	return &t, nil
}

func (h *schemaHandlers) sync(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSON[schemaSyncRequest](r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "malformed JSON body")
		return
	}

	scheduledAt, err := parseSchedule(req.Schedule)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}

	jobID, err := h.deps.Sync.StartSync(r.Context(), req.Tenant, req.Module, scheduledAt)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}

	writeData(w, http.StatusAccepted, struct {
		JobID string `json:"job_id"`
	}{JobID: jobID})
}

type schemaAcceptRequest struct {
	Module string `json:"module"`
	Tenant string `json:"tenant"`
	Reason string `json:"reason"`
}

func (h *schemaHandlers) accept(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSON[schemaAcceptRequest](r)
	if err != nil || req.Module == "" || req.Tenant == "" || req.Reason == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "module, tenant, and reason are required")
		return
	}

	acceptanceIDs, jobID, err := h.deps.Accept.Accept(r.Context(), req.Tenant, req.Module, req.Reason, operatorIdentity(r))
	if err != nil {
		if errors.Is(err, tenantsync.ErrNothingBlocked) {
			writeError(w, http.StatusConflict, "nothing_blocked", err.Error())
			return
		}
		writeSchemaResolveError(w, err)
		return
	}

	// AcceptanceID (cli-reference.md §4's documented singular field, the
	// common case: one blocked change, one acceptance) is always the
	// first id when there's at least one. AcceptanceIDs carries every id
	// unconditionally (not just when there's more than one) — a client
	// that reads only the documented singular field still gets a real,
	// usable id, but a client that recorded more than one blocked change
	// this call has a reliable field to find every id in rather than
	// silently losing all but the first past the one the plain-singular
	// contract can express.
	resp := struct {
		JobID         string   `json:"job_id"`
		AcceptanceID  string   `json:"acceptance_id,omitempty"`
		AcceptanceIDs []string `json:"acceptance_ids,omitempty"`
	}{JobID: jobID, AcceptanceIDs: acceptanceIDs}
	if len(acceptanceIDs) > 0 {
		resp.AcceptanceID = acceptanceIDs[0]
	}
	writeData(w, http.StatusAccepted, resp)
}
