// Package adminapi's tenant routes wrap internal/engine/tenant.Store for
// the reads and status-only writes that are buildable now (list, status,
// suspend, unsuspend), and gate everything that needs a not-yet-built
// subsystem — ProvisionTenantWorkflow, the invite system, River,
// OffboardTenantWorkflow — behind small interfaces (Provisioner,
// InviteResender, TenantExporter, TenantImporter, Offboarder). A nil
// dependency isn't a bug: engine.go simply hasn't wired a real
// implementation in yet, and the handler reports that honestly via
// writeNotImplemented rather than pretending to succeed. Once the
// corresponding tracking issue lands, wiring a real implementation into
// TenantDeps in engine.go is the only change needed — these handlers
// don't change.
package adminapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/role"
	"github.com/djangbahevans/goerp/internal/engine/schema"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	"github.com/djangbahevans/goerp/internal/engine/user"
)

type TenantDeps struct {
	Store       *tenant.Store
	SyncStatus  SyncStatusReader
	TableCounts TableCounter
	Membership  TenantMembership
	Users       UserResolver
	Provisioner Provisioner
	Inviter     InviteResender
	Exporter    TenantExporter
	Importer    TenantImporter
	Offboarder  Offboarder
}

func RegisterTenantRoutes(mux *http.ServeMux, deps TenantDeps) {
	h := &tenantHandlers{deps: deps}
	mux.HandleFunc("GET /admin/tenants", h.list)
	mux.HandleFunc("POST /admin/tenants", h.create)
	mux.HandleFunc("GET /admin/tenants/{slug}", h.status)
	mux.HandleFunc("POST /admin/tenants/{slug}/suspend", h.suspend)
	mux.HandleFunc("POST /admin/tenants/{slug}/unsuspend", h.unsuspend)
	mux.HandleFunc("POST /admin/tenants/{slug}/resend-invite", h.resendInvite)
	mux.HandleFunc("POST /admin/tenants/{slug}/export", h.export)
	mux.HandleFunc("POST /admin/tenants/import", h.importTenant)
	mux.HandleFunc("POST /admin/tenants/{slug}/offboard", h.offboard)
	mux.HandleFunc("POST /admin/tenants/{slug}/offboard/cancel", h.offboardCancel)
}

// SyncStatusReader is satisfied by *schema.SchemaSyncPool (StatusForTenant)
// — the status route's "N of M modules synced" ratio is computed purely
// from a tenant's own module_schema_versions rows, not against a live
// module registry, so it needs no dependency on Stage 3 module-loading
// orchestration (goerp#159) landing first.
type SyncStatusReader interface {
	StatusForTenant(ctx context.Context, tenantID string) ([]schema.ModuleSyncStatus, error)
}

// TableCounter is satisfied by *schema.SchemaSyncPool (TableCount) — the
// status route's schema table count, computed directly against
// information_schema.tables for the tenant's own Postgres schema.
type TableCounter interface {
	TableCount(ctx context.Context, tenantSlug string) (int, error)
}

// TenantMembership is satisfied by *role.Store (CountUsers, AdminUserID) —
// both derived from the tenant's own tenant_{slug}.user_roles/roles
// tables, not a system-wide table, since tenant membership is only
// recorded per-tenant-schema (goerp#3523's RBAC tables). Kept as its own
// interface rather than folded into SyncStatusReader/TableCounter since it
// is satisfied by a different concrete store.
type TenantMembership interface {
	CountUsers(ctx context.Context, tenantSlug string) (int, error)
	AdminUserID(ctx context.Context, tenantSlug string) (string, error)
}

// UserResolver is satisfied by *user.Store (GetByID) — resolves the id
// TenantMembership.AdminUserID returns into the email the status route
// reports. system.users has no display-name column yet, so admin_user
// only ever carries id/email, not a name.
type UserResolver interface {
	GetByID(ctx context.Context, id string) (*user.User, error)
}

type CreateTenantRequest struct {
	Slug       string   `json:"slug"`
	Name       string   `json:"name"`
	Plan       string   `json:"plan"`
	AdminEmail string   `json:"admin_email"`
	AdminName  string   `json:"admin_name"`
	Region     string   `json:"region"`
	Country    string   `json:"country"`
	Modules    []string `json:"modules"`
}

type Provisioner interface {
	StartProvisioning(ctx context.Context, req CreateTenantRequest) (jobID string, err error)
}
type InviteResender interface {
	ResendInvite(ctx context.Context, tenantSlug, email string) error
}
type TenantExporter interface {
	StartExport(ctx context.Context, tenantSlug string, include, exclude []string) (jobID string, err error)
}
type TenantImporter interface {
	StartImport(ctx context.Context, newSlug, inputRef, decryptionKey string) (jobID string, err error)
}

// OffboardResult mirrors cli-reference.md's two `tenant offboard` response
// shapes: the grace-period path is synchronous and returns Status/DeleteAt,
// the immediate path is async and returns JobID instead.
type OffboardResult struct {
	Status   string     `json:"status"` // "scheduled" | "accepted"
	DeleteAt *time.Time `json:"delete_at,omitempty"`
	JobID    string     `json:"job_id,omitempty"`
}

type Offboarder interface {
	StartOffboard(ctx context.Context, tenantSlug string, gracePeriod time.Duration, immediate bool) (result OffboardResult, err error)
	CancelOffboard(ctx context.Context, tenantSlug string) error
}

type tenantHandlers struct {
	deps TenantDeps
}

func decodeJSON[T any](r *http.Request) (T, error) {
	var v T
	err := json.NewDecoder(r.Body).Decode(&v)
	return v, err
}

// tenantListItem is tenant.Tenant plus the Users column cli-reference.md
// §5 documents for `goerp tenant list`.
type tenantListItem struct {
	tenant.Tenant
	Users int `json:"users"`
}

func (h *tenantHandlers) list(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))

	tenants, nextCursor, err := h.deps.Store.List(r.Context(), tenant.ListFilter{
		Status: tenant.Status(r.URL.Query().Get("filter")),
		Plan:   tenant.Plan(r.URL.Query().Get("plan")),
		Limit:  limit,
		Cursor: r.URL.Query().Get("cursor"),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}

	items := make([]tenantListItem, len(tenants))
	for i, t := range tenants {
		item := tenantListItem{Tenant: t}
		if h.deps.Membership != nil {
			count, err := h.deps.Membership.CountUsers(r.Context(), t.Slug)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "internal", err.Error())
				return
			}
			item.Users = count
		}
		items[i] = item
	}

	writeData(w, http.StatusOK, struct {
		Tenants    []tenantListItem `json:"tenants"`
		NextCursor string           `json:"next_cursor,omitempty"`
	}{Tenants: items, NextCursor: nextCursor})
}

func (h *tenantHandlers) create(w http.ResponseWriter, r *http.Request) {
	if h.deps.Provisioner == nil {
		writeNotImplemented(w, "goerp#149")
		return
	}

	req, err := decodeJSON[CreateTenantRequest](r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "malformed JSON body")
		return
	}
	if req.Slug == "" || req.AdminEmail == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "slug and admin_email are required")
		return
	}

	workflowID, err := h.deps.Provisioner.StartProvisioning(r.Context(), req)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}

	writeData(w, http.StatusAccepted, struct {
		Slug       string `json:"slug"`
		WorkflowID string `json:"workflow_id"`
	}{Slug: req.Slug, WorkflowID: workflowID})
}

// tenantStatusResponse is Tenant plus the "N of M modules synced" ratio,
// schema table count, provisioning duration, and admin user (§5 "goerp
// tenant status") — computed from SyncStatusReader/TableCounter/
// TenantMembership/UserResolver, not from a live module registry (see
// SyncStatusReader's doc comment).
type tenantStatusResponse struct {
	tenant.Tenant
	SchemaTableCount     int                       `json:"schema_table_count,omitempty"`
	ModulesSynced        int                       `json:"modules_synced"`
	ModulesTotal         int                       `json:"modules_total"`
	Modules              []schema.ModuleSyncStatus `json:"modules,omitempty"`
	ProvisioningDuration string                    `json:"provisioning_duration,omitempty"`
	AdminUser            *AdminUserInfo            `json:"admin_user,omitempty"`
}

// AdminUserInfo is the admin_user field of `goerp tenant status`'s output.
// system.users has no display-name column yet, so this only ever carries
// id/email, not a name.
type AdminUserInfo struct {
	ID    string `json:"id"`
	Email string `json:"email"`
}

func (h *tenantHandlers) status(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")

	t, err := h.deps.Store.GetBySlug(r.Context(), slug)
	if err != nil {
		if errors.Is(err, tenant.ErrTenantNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "tenant not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}

	resp := tenantStatusResponse{Tenant: *t}
	if h.deps.SyncStatus != nil {
		statuses, err := h.deps.SyncStatus.StatusForTenant(r.Context(), t.ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal", err.Error())
			return
		}
		resp.Modules = statuses
		resp.ModulesTotal = len(statuses)
		for _, s := range statuses {
			if s.Status == "ok" {
				resp.ModulesSynced++
			}
		}
	}

	if h.deps.TableCounts != nil {
		count, err := h.deps.TableCounts.TableCount(r.Context(), t.Slug)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal", err.Error())
			return
		}
		resp.SchemaTableCount = count
	}

	if t.ActivatedAt != nil {
		resp.ProvisioningDuration = t.ActivatedAt.Sub(t.CreatedAt).String()
	}

	if h.deps.Membership != nil {
		adminUserID, err := h.deps.Membership.AdminUserID(r.Context(), t.Slug)
		switch {
		case err == nil:
			if h.deps.Users != nil {
				admin, err := h.deps.Users.GetByID(r.Context(), adminUserID)
				if err != nil {
					if !errors.Is(err, user.ErrUserNotFound) {
						writeError(w, http.StatusInternalServerError, "internal", err.Error())
						return
					}
				} else {
					resp.AdminUser = &AdminUserInfo{ID: admin.ID, Email: admin.Email}
				}
			}
		case errors.Is(err, role.ErrAdminUserNotFound):
			// No user holds the admin role for this tenant yet.
		default:
			writeError(w, http.StatusInternalServerError, "internal", err.Error())
			return
		}
	}

	writeData(w, http.StatusOK, resp)
}

func (h *tenantHandlers) suspend(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")

	req, err := decodeJSON[struct {
		Reason string `json:"reason"`
	}](r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "malformed JSON body")
		return
	}
	if req.Reason == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "reason is required")
		return
	}

	t, err := h.deps.Store.UpdateStatus(r.Context(), slug, tenant.StatusSuspended, &req.Reason)
	if err != nil {
		if errors.Is(err, tenant.ErrTenantNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "tenant not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}

	writeData(w, http.StatusOK, t)
}

func (h *tenantHandlers) unsuspend(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")

	t, err := h.deps.Store.UpdateStatus(r.Context(), slug, tenant.StatusActive, nil)
	if err != nil {
		if errors.Is(err, tenant.ErrTenantNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "tenant not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}

	writeData(w, http.StatusOK, t)
}

func (h *tenantHandlers) resendInvite(w http.ResponseWriter, r *http.Request) {
	if h.deps.Inviter == nil {
		writeNotImplemented(w, "goerp#148")
		return
	}

	slug := r.PathValue("slug")
	req, err := decodeJSON[struct {
		Email string `json:"email"`
	}](r)
	if err != nil || req.Email == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "email is required")
		return
	}

	if err := h.deps.Inviter.ResendInvite(r.Context(), slug, req.Email); err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}

	writeData(w, http.StatusOK, struct {
		Status string `json:"status"`
	}{Status: "sent"})
}

func (h *tenantHandlers) export(w http.ResponseWriter, r *http.Request) {
	if h.deps.Exporter == nil {
		writeNotImplemented(w, "goerp#15")
		return
	}

	slug := r.PathValue("slug")
	req, err := decodeJSON[struct {
		Include []string `json:"include"`
		Exclude []string `json:"exclude"`
	}](r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "malformed JSON body")
		return
	}

	jobID, err := h.deps.Exporter.StartExport(r.Context(), slug, req.Include, req.Exclude)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}

	writeData(w, http.StatusAccepted, struct {
		JobID string `json:"job_id"`
	}{JobID: jobID})
}

func (h *tenantHandlers) importTenant(w http.ResponseWriter, r *http.Request) {
	if h.deps.Importer == nil {
		writeNotImplemented(w, "goerp#15")
		return
	}

	req, err := decodeJSON[struct {
		Slug          string `json:"slug"`
		Input         string `json:"input"`
		DecryptionKey string `json:"decryption_key"`
	}](r)
	if err != nil || req.Slug == "" || req.Input == "" || req.DecryptionKey == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "slug, input, and decryption_key are required")
		return
	}

	jobID, err := h.deps.Importer.StartImport(r.Context(), req.Slug, req.Input, req.DecryptionKey)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}

	writeData(w, http.StatusAccepted, struct {
		JobID string `json:"job_id"`
	}{JobID: jobID})
}

func (h *tenantHandlers) offboard(w http.ResponseWriter, r *http.Request) {
	if h.deps.Offboarder == nil {
		writeNotImplemented(w, "goerp#150")
		return
	}

	slug := r.PathValue("slug")
	req, err := decodeJSON[struct {
		GracePeriodDays int    `json:"grace_period_days"`
		Immediate       bool   `json:"immediate"`
		Confirm         string `json:"confirm"`
	}](r)
	if err != nil || req.Confirm != slug {
		writeError(w, http.StatusBadRequest, "invalid_request", "confirm must equal the tenant slug")
		return
	}

	result, err := h.deps.Offboarder.StartOffboard(r.Context(), slug, time.Duration(req.GracePeriodDays)*24*time.Hour, req.Immediate)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}

	status := http.StatusOK
	if req.Immediate {
		status = http.StatusAccepted
	}
	writeData(w, status, result)
}

func (h *tenantHandlers) offboardCancel(w http.ResponseWriter, r *http.Request) {
	if h.deps.Offboarder == nil {
		writeNotImplemented(w, "goerp#150")
		return
	}

	slug := r.PathValue("slug")
	if err := h.deps.Offboarder.CancelOffboard(r.Context(), slug); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}

	writeData(w, http.StatusOK, struct {
		Status string `json:"status"`
	}{Status: "cancelled"})
}
