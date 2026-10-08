// Package authaudit manages the platform-wide, monthly-partitioned authentication audit
// log and its event write path.
package authaudit

import (
	"context"
	"database/sql"
	"encoding/json/v2"
	"fmt"

	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
)

// Postgres requires the partition key in every unique constraint, so the primary key
// includes emitted_at.
const createAuthAuditLogTable = `
CREATE TABLE IF NOT EXISTS system.auth_audit_log (
    id              UUID NOT NULL DEFAULT uuidv7(),
    event_type      TEXT NOT NULL,
    tenant_id       UUID REFERENCES system.tenants(id),
    user_id         UUID,
    actor_user_id   UUID,
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

// A BRIN index suits append-only time-series data ordered by the partition column.
const createAuthAuditLogTimeIndex = `
CREATE INDEX IF NOT EXISTS idx_auth_audit_log_time ON system.auth_audit_log USING BRIN (created_at)
`

// createAuthAuditLogUserIndex and createAuthAuditLogActorIndex serve the
// per-user activity read (auth-internals.md §17 "Tenant admin activity
// read API"): what was done to a user's account, and what they did.
const createAuthAuditLogUserIndex = `
CREATE INDEX IF NOT EXISTS idx_auth_audit_log_user ON system.auth_audit_log (tenant_id, user_id, created_at DESC, id DESC)
`

const createAuthAuditLogActorIndex = `
CREATE INDEX IF NOT EXISTS idx_auth_audit_log_actor ON system.auth_audit_log (tenant_id, actor_user_id, created_at DESC, id DESC)
`

type Store struct {
	db          *sql.DB
	tenantStore *tenant.Store
}

func NewStore(db *sql.DB, tenantStore *tenant.Store) *Store {
	return &Store{db: db, tenantStore: tenantStore}
}

// Bootstrap creates and registers the platform-wide auth audit table with pg_partman.
// Table creation and partition registration share an advisory-locked transaction to
// serialize engine replicas.
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
		if _, err := tx.ExecContext(ctx, createAuthAuditLogUserIndex); err != nil {
			return fmt.Errorf("create auth_audit_log user index: %w", err)
		}
		if _, err := tx.ExecContext(ctx, createAuthAuditLogActorIndex); err != nil {
			return fmt.Errorf("create auth_audit_log actor index: %w", err)
		}
		return db.RegisterPartition(ctx, tx, "system.auth_audit_log", "created_at")
	})
}

// Row is one auth_audit_log entry. TenantID/UserID/ActorUserID/SessionID/
// APIKeyID are "" when not applicable to EventType — stored as SQL NULL,
// not an empty UUID. UserID is the account the event is about and
// ActorUserID the signed-in user whose request caused it
// (auth-internals.md §17 "Who an event is about, and who caused it"); the
// actor is never repeated in Metadata. Metadata is pre-marshaled JSON, nil
// when the event carries no event-specific detail.
type Row struct {
	EventType     string
	TenantID      string
	UserID        string
	ActorUserID   string
	SessionID     string
	APIKeyID      string
	IPAddress     string
	UserAgent     string
	CountryCode   string
	Success       bool
	FailureReason string
	Metadata      []byte
}

// Insert appends an authentication audit row.
func (s *Store) Insert(ctx context.Context, row Row) error {
	return insertRow(ctx, s.db, row)
}

// InsertTx is Insert inside the caller's transaction, for an event that
// must commit or roll back with the state change it records
// (auth-internals.md §2 "Transactional audit").
func (s *Store) InsertTx(ctx context.Context, tx *sql.Tx, row Row) error {
	return insertRow(ctx, tx, row)
}

func insertRow(ctx context.Context, q db.Execer, row Row) error {
	_, err := q.ExecContext(ctx, `
		INSERT INTO system.auth_audit_log
			(event_type, tenant_id, user_id, actor_user_id, session_id, api_key_id, ip_address, user_agent, country_code, success, failure_reason, metadata)
		VALUES ($1, NULLIF($2, '')::uuid, NULLIF($3, '')::uuid, NULLIF($4, '')::uuid, NULLIF($5, '')::uuid, NULLIF($6, '')::uuid, NULLIF($7, '')::inet, NULLIF($8, ''), NULLIF($9, ''), $10, NULLIF($11, ''), $12)
	`, row.EventType, row.TenantID, row.UserID, row.ActorUserID, row.SessionID, row.APIKeyID, row.IPAddress, row.UserAgent, row.CountryCode, row.Success, row.FailureReason, row.Metadata)
	if err != nil {
		return fmt.Errorf("insert auth_audit_log row: %w", err)
	}
	return nil
}

// Emit resolves the tenant slug and records a successful invite transition. userID
// identifies the account and actorUserID the acting user; empty IDs represent absent
// identities.
func (s *Store) Emit(ctx context.Context, tenantSlug, eventName, userID, actorUserID string, payload map[string]any) error {
	t, err := s.tenantStore.GetBySlug(ctx, tenantSlug)
	if err != nil {
		return fmt.Errorf("resolve tenant %s: %w", tenantSlug, err)
	}

	var metadata []byte
	if payload != nil {
		if metadata, err = json.Marshal(payload); err != nil {
			return fmt.Errorf("marshal metadata: %w", err)
		}
	}

	return s.Insert(ctx, Row{
		EventType:   eventName,
		TenantID:    t.ID,
		UserID:      userID,
		ActorUserID: actorUserID,
		Success:     true,
		Metadata:    metadata,
	})
}

// EventExists checks JSONB metadata for a matching event. Callers can use it to suppress
// repeated notices; the query is unindexed and unsuitable for a high-frequency path.
func (s *Store) EventExists(ctx context.Context, eventType, metadataKey, metadataValue string) (bool, error) {
	var exists bool
	err := s.db.QueryRowContext(ctx, `
		SELECT EXISTS(SELECT 1 FROM system.auth_audit_log WHERE event_type = $1 AND metadata->>$2 = $3)
	`, eventType, metadataKey, metadataValue).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("check existing %s event: %w", eventType, err)
	}
	return exists, nil
}
