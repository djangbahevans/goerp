package adminapi

import (
	"context"
	"database/sql"
	"encoding/json/v2"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
	"uuid"

	"github.com/djangbahevans/goerp/internal/engine/authaudit"
	"github.com/djangbahevans/goerp/internal/engine/httperr"
	"github.com/djangbahevans/goerp/internal/engine/jobqueue"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"github.com/rs/zerolog/log"
	"github.com/vmihailenco/msgpack/v5"
)

type AccountSessions interface {
	RevokeAllForUserTx(context.Context, *sql.Tx, string, string) ([]string, error)
	Blocklist(context.Context, []string) error
}

type AccountAudit interface {
	InsertTx(context.Context, *sql.Tx, authaudit.Row) error
}

type AccountJobs interface {
	InsertTx(context.Context, *sql.Tx, river.JobArgs, *river.InsertOpts) (*rivertype.JobInsertResult, error)
}

type AccountMailer interface {
	SendOperatorMFAReset(context.Context, string) error
}

type AccountDeps struct {
	DB       *sql.DB
	Sessions AccountSessions
	Audit    AccountAudit
	Jobs     AccountJobs
	Mailer   AccountMailer
}

func RegisterAccountRoutes(mux *http.ServeMux, deps AccountDeps) {
	h := &accountHandlers{deps: deps}
	mux.HandleFunc("POST /admin/accounts/{id}/suspend", h.suspend)
	mux.HandleFunc("POST /admin/accounts/{id}/unsuspend", func(w http.ResponseWriter, r *http.Request) {
		h.change(w, r, "unsuspend", "")
	})
	mux.HandleFunc("DELETE /admin/accounts/{id}", func(w http.ResponseWriter, r *http.Request) {
		h.change(w, r, "delete", "")
	})
	mux.HandleFunc("POST /admin/accounts/{id}/mfa/reset", func(w http.ResponseWriter, r *http.Request) {
		h.change(w, r, "mfa/reset", "")
	})
}

type accountHandlers struct{ deps AccountDeps }

type accountTenant struct{ id, slug string }

type accountPreview struct {
	Email        string   `json:"email"`
	Tenants      []string `json:"tenants"`
	LiveSessions int      `json:"live_sessions"`
}

func (h *accountHandlers) suspend(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSON[struct {
		Reason string `json:"reason"`
	}](r)
	if err != nil || strings.TrimSpace(req.Reason) == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "a reason is required")
		return
	}

	h.change(w, r, "suspend", req.Reason)
}

func (h *accountHandlers) change(w http.ResponseWriter, r *http.Request, action, reason string) {
	id := r.PathValue("id")
	if _, err := uuid.Parse(id); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "invalid user ID")
		return
	}

	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "malformed query parameters")
		return
	}

	dryRun := false
	if values, ok := query["dry_run"]; ok {
		if (action != "delete" && action != "mfa/reset") || len(values) != 1 || (values[0] != "true" && values[0] != "false") {
			writeError(w, http.StatusBadRequest, "invalid_request", "dry_run must be true or false and is supported only for delete and MFA reset")
			return
		}

		dryRun = values[0] == "true"
	}

	ctx := r.Context()
	tx, err := h.deps.DB.BeginTx(ctx, nil)
	if err != nil {
		h.failed(w, err)
		return
	}

	defer func() { _ = tx.Rollback() }()

	var email, status string
	var deletedAt *time.Time

	// The account lock serializes MFA enrollment, verification and operator changes.
	err = tx.QueryRowContext(ctx, `SELECT email, status, deleted_at FROM system.users WHERE id = $1 FOR NO KEY UPDATE`, id).Scan(&email, &status, &deletedAt)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "not_found", "account not found")
		return
	}

	if err != nil {
		h.failed(w, err)
		return
	}

	if action == "suspend" && (status != "active" || deletedAt != nil) {
		writeError(w, http.StatusConflict, "user_not_active", "account is not active")
		return
	}

	if action == "unsuspend" && (status != "suspended" || deletedAt != nil) {
		writeError(w, http.StatusConflict, "user_not_suspended", "account is not suspended")
		return
	}

	if deletedAt != nil {
		if action == "delete" && !dryRun {
			w.WriteHeader(http.StatusNoContent)
		} else {
			writeError(w, http.StatusNotFound, "not_found", "account is deleted")
		}

		return
	}

	tenants, err := accountTenants(ctx, tx, id)
	if err != nil {
		h.failed(w, err)
		return
	}

	if dryRun {
		preview := accountPreview{Email: email, Tenants: []string{}}
		for _, tenant := range tenants {
			preview.Tenants = append(preview.Tenants, tenant.slug)
		}

		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM system.sessions WHERE user_id = $1 AND revoked_at IS NULL AND expires_at > NOW()`, id).Scan(&preview.LiveSessions); err != nil {
			h.failed(w, err)
			return
		}

		writeData(w, http.StatusOK, preview)
		return
	}

	var revokedIDs []string
	if action != "unsuspend" {
		revokedIDs, err = h.deps.Sessions.RevokeAllForUserTx(ctx, tx, id, "operator_"+strings.ReplaceAll(action, "/", "_"))
		if err != nil {
			h.failed(w, err)
			return
		}
	}

	revokedIDs, err = accountBlocklistIDs(ctx, tx, id, revokedIDs)
	if err != nil {
		h.failed(w, err)
		return
	}

	var eventName string
	switch action {
	case "suspend":
		eventName = "account.suspended"
		_, err = tx.ExecContext(ctx, `UPDATE system.users SET status = 'suspended', updated_at = NOW() WHERE id = $1 AND status = 'active' AND deleted_at IS NULL`, id)

	case "unsuspend":
		eventName = "account.unsuspended"
		_, err = tx.ExecContext(ctx, `UPDATE system.users SET status = 'active', updated_at = NOW() WHERE id = $1 AND status = 'suspended' AND deleted_at IS NULL`, id)

	case "delete":
		eventName = "account.deleted"
		_, err = tx.ExecContext(ctx, `UPDATE system.users SET status = 'deleted', deleted_at = NOW(), updated_at = NOW() WHERE id = $1 AND deleted_at IS NULL`, id)

	case "mfa/reset":
		eventName = "mfa.operator_reset"
		_, err = tx.ExecContext(ctx, `UPDATE system.user_mfa SET revoked_at = NOW() WHERE user_id = $1 AND revoked_at IS NULL`, id)
		if err == nil {
			for _, tenant := range tenants {
				if _, err = tx.ExecContext(ctx, `UPDATE `+tenantschema.Name(tenant.slug)+`.tenant_members SET mfa_reset_at = NULL WHERE user_id = $1`, id); err != nil {
					break
				}
			}
		}
	}

	if err != nil {
		h.failed(w, err)
		return
	}

	var metadata []byte
	if reason != "" {
		metadata, err = json.Marshal(map[string]string{"reason": reason})
		if err != nil {
			h.failed(w, err)
			return
		}
	}

	if err := h.deps.Audit.InsertTx(ctx, tx, authaudit.Row{
		EventType: eventName,
		UserID:    id,
		Success:   true,
		Metadata:  metadata,
	}); err != nil {
		h.failed(w, err)
		return
	}

	if action == "delete" || action == "suspend" {
		payload := map[string]any{"user_id": id, "email": email}
		name := "system.user.deleted"
		if action == "suspend" {
			name = "system.user.suspended"
			payload = map[string]any{"user_id": id, "reason": reason, "suspended_by": nil, "scope": "account"}
		}

		encoded, err := msgpack.Marshal(payload)
		if err != nil {
			h.failed(w, err)
			return
		}

		for _, tenant := range tenants {
			_, err := h.deps.Jobs.InsertTx(ctx, tx, jobqueue.EventDeliveryArgs{
				EventID:       uuid.NewV7().String(),
				EventName:     name,
				EventVersion:  1,
				EmitterModule: "system",
				TenantID:      tenant.id,
				TraceID:       httperr.TraceIDFromContext(ctx),
				Payload:       encoded,
				EmittedAt:     time.Now(),
				Transactional: true,
			}, nil)
			if err != nil {
				h.failed(w, err)
				return
			}
		}
	}

	// Reactivating an account must not restore tokens missed by a failed suspension blocklist.
	if action == "unsuspend" {
		if err := h.deps.Sessions.Blocklist(ctx, revokedIDs); err != nil {
			h.failed(w, err)
			return
		}
	}

	if err := tx.Commit(); err != nil {
		h.failed(w, err)
		return
	}

	var blocklistErr error
	if action != "unsuspend" {
		blocklistErr = h.deps.Sessions.Blocklist(ctx, revokedIDs)
	}

	if action == "mfa/reset" {
		if err := h.deps.Mailer.SendOperatorMFAReset(ctx, email); err != nil {
			log.Error().Err(err).Str("user_id", id).Msg("operator MFA reset notification failed")
		}
	}

	if blocklistErr != nil {
		writeError(w, http.StatusInternalServerError, "internal", "account change committed, but blocking outstanding access tokens failed")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *accountHandlers) failed(w http.ResponseWriter, err error) {
	log.Error().Err(err).Msg("operator account change failed")
	writeError(w, http.StatusInternalServerError, "internal", "account change failed")
}

func accountTenants(ctx context.Context, tx *sql.Tx, id string) ([]accountTenant, error) {
	rows, err := tx.QueryContext(ctx, `SELECT t.id, t.slug FROM system.tenant_memberships m JOIN system.tenants t ON t.id = m.tenant_id WHERE m.user_id = $1 ORDER BY t.slug`, id)
	if err != nil {
		return nil, fmt.Errorf("list account memberships: %w", err)
	}

	defer func() { _ = rows.Close() }()
	var tenants []accountTenant
	for rows.Next() {
		var tenant accountTenant
		if err := rows.Scan(&tenant.id, &tenant.slug); err != nil {
			return nil, fmt.Errorf("scan account membership: %w", err)
		}

		tenants = append(tenants, tenant)
	}

	return tenants, rows.Err()
}

// Retrying after a Redis failure must also block tokens for sessions already revoked in Postgres.
func accountBlocklistIDs(ctx context.Context, tx *sql.Tx, userID string, ids []string) ([]string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id FROM system.sessions
		WHERE user_id = $1 AND revoked_at IS NOT NULL AND expires_at > NOW()`, userID)
	if err != nil {
		return nil, fmt.Errorf("list revoked account sessions: %w", err)
	}

	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan revoked account session: %w", err)
		}

		if !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}

	return ids, rows.Err()
}
