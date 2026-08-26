// Package authaudit is the system-schema auth_audit_log table
// (auth-internals.md §17 "Audit trail for auth events") — a monthly-
// partitioned, platform-wide table distinct from the per-tenant business
// audit_log (internal/engine/dataaudit/host.orm write path, goerp#363)
// and the admin_audit_log table (internal/engine/auditlog, goerp#... admin
// API request log). Scoped to table creation, partitioning, and a single
// Insert/Emit write path (goerp#400) — instrumenting the ~30 documented
// auth event types into their actual call sites, high-volume dedup, the
// immutability trigger, and legal holds are separate, still-unfiled
// tickets (backlog #298/#299/#300/#301).
package authaudit

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
)

// createAuthAuditLogTable mirrors auth-internals.md §17's schema exactly,
// except the PK — id alone there (id UUID PRIMARY KEY) can't hold on a
// partitioned table, since Postgres requires the partition key in every
// unique constraint (the same reason goerp#194 moved event_log/audit_log
// to a composite PK). No REVOKE SELECT/immutability trigger yet — that's
// backlog #300's own scope, not built here.
const createAuthAuditLogTable = `
CREATE TABLE IF NOT EXISTS system.auth_audit_log (
    id              UUID NOT NULL DEFAULT uuidv7(),
    event_type      TEXT NOT NULL,
    tenant_id       UUID REFERENCES system.tenants(id),
    user_id         UUID,
    session_id      UUID,
    api_key_id      UUID,
    ip_address      INET,
    user_agent      TEXT,
    country_code    CHAR(2),
    success         BOOLEAN NOT NULL,
    failure_reason  TEXT,
    metadata        JSONB,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (id, created_at)
) PARTITION BY RANGE (created_at)
`

// createAuthAuditLogTimeIndex mirrors event_log/audit_log's own BRIN
// index on their partition column (goerp#194) — efficient for append-only
// time-series data, same reasoning as those two tables.
const createAuthAuditLogTimeIndex = `
CREATE INDEX IF NOT EXISTS idx_auth_audit_log_time ON system.auth_audit_log USING BRIN (created_at)
`

type Store struct {
	db          *sql.DB
	tenantStore *tenant.Store
}

func NewStore(db *sql.DB, tenantStore *tenant.Store) *Store {
	return &Store{db: db, tenantStore: tenantStore}
}

// Bootstrap creates system.auth_audit_log (and its index) if it doesn't
// already exist, and registers it with pg_partman. Idempotent — safe to
// call on every engine startup, same as tenant.Store.Bootstrap.
// Concurrent-safe against other processes calling Bootstrap at the same
// time (goerp#171) via db.WithAdvisoryLock — unlike tenant provisioning's
// own per-tenant partition registration (naturally serialized by Temporal
// running at most one CreateEngineTables attempt per tenant at a time),
// Bootstrap runs at every engine startup, so a real deployment with
// multiple engine replicas can call it concurrently. db.RegisterPartition
// runs inside the same locked transaction as the table's own creation for
// exactly that reason — a second concurrent caller blocks on the
// advisory lock until the first commits, then sees the table (and, once
// registered, the partman.part_config row) already there and no-ops,
// rather than racing the first caller's own check-then-create_parent.
// Unlike per-tenant partitioned tables (registered once per tenant, at
// provisioning time), this table is platform-wide — Bootstrap only ever
// registers it once, here, not per tenant.
func (s *Store) Bootstrap(ctx context.Context) error {
	keys := []int64{db.SystemSchemaLockKey, db.AdvisoryLockKey("authaudit.Bootstrap")}
	return db.WithAdvisoryLock(ctx, s.db, keys, func(tx *sql.Tx) error {
		if err := db.EnsureSystemSchema(ctx, tx); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, createAuthAuditLogTable); err != nil {
			return fmt.Errorf("create auth_audit_log table: %w", err)
		}
		if _, err := tx.ExecContext(ctx, createAuthAuditLogTimeIndex); err != nil {
			return fmt.Errorf("create auth_audit_log time index: %w", err)
		}
		return db.RegisterPartition(ctx, tx, "system.auth_audit_log", "created_at")
	})
}

// Row is one auth_audit_log entry. TenantID/UserID/SessionID/APIKeyID are
// "" when not applicable to EventType — stored as SQL NULL, not an empty
// UUID. Metadata is pre-marshaled JSON, nil when the event carries no
// event-specific detail.
type Row struct {
	EventType     string
	TenantID      string
	UserID        string
	SessionID     string
	APIKeyID      string
	IPAddress     string
	UserAgent     string
	CountryCode   string
	Success       bool
	FailureReason string
	Metadata      []byte
}

// Insert writes one row. There is no corresponding Update or Delete —
// auth_audit_log is append-only by convention here (immutability at the
// database-privilege level is backlog #300's own scope, not enforced
// yet).
func (s *Store) Insert(ctx context.Context, row Row) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO system.auth_audit_log
			(event_type, tenant_id, user_id, session_id, api_key_id, ip_address, user_agent, country_code, success, failure_reason, metadata)
		VALUES ($1, NULLIF($2, '')::uuid, NULLIF($3, '')::uuid, NULLIF($4, '')::uuid, NULLIF($5, '')::uuid, NULLIF($6, '')::inet, NULLIF($7, ''), NULLIF($8, ''), $9, NULLIF($10, ''), $11)
	`, row.EventType, row.TenantID, row.UserID, row.SessionID, row.APIKeyID, row.IPAddress, row.UserAgent, row.CountryCode, row.Success, row.FailureReason, row.Metadata)
	if err != nil {
		return fmt.Errorf("insert auth_audit_log row: %w", err)
	}
	return nil
}

// Emit satisfies invite.AuditEmitter — resolves tenantSlug to a tenant_id,
// marshals payload into metadata, and writes a row with Success true
// (every invite event type this interface carries — user.invited,
// user.invite_accepted, user.invite_resent, user.invite_revoked,
// user.invite_expired — is an unconditional state transition, never a
// pass/fail outcome the way login.failure or mfa.failed are).
func (s *Store) Emit(ctx context.Context, tenantSlug, eventName string, payload map[string]any) error {
	t, err := s.tenantStore.GetBySlug(ctx, tenantSlug)
	if err != nil {
		return fmt.Errorf("resolve tenant %s: %w", tenantSlug, err)
	}

	metadata, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal metadata: %w", err)
	}

	return s.Insert(ctx, Row{
		EventType: eventName,
		TenantID:  t.ID,
		Success:   true,
		Metadata:  metadata,
	})
}
