// Package auditlog records one immutable row per mutating admin API
// request (engine-internals.md §11 "Audit logging"). "Immutable" here is
// enforced by omission, not a database constraint: Store exposes Write and
// nothing else — there is no Update or Delete, the same reasoning
// schema_sync_acceptances already relies on for its own append-only rows.
package auditlog

import (
	"context"
	"database/sql"
	"fmt"
)

const createSchema = `CREATE SCHEMA IF NOT EXISTS system`

const createAuditLogTable = `
CREATE TABLE IF NOT EXISTS system.admin_audit_log (
    id                UUID PRIMARY KEY DEFAULT uuidv7(),
    operator_identity TEXT NOT NULL,
    endpoint          TEXT NOT NULL,
    target_scope      TEXT NOT NULL DEFAULT '',
    idempotency_key   TEXT,
    job_id            TEXT,
    status_code       INT NOT NULL,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
)
`

// createAuditLogCreatedAtIndex supports the retention/listing queries
// cli-reference.md's admin-audit-adjacent commands run against this table
// (newest first, optionally bounded by a time range) — the same
// "index the access pattern, not just the primary key" reasoning as
// idx_tenant_domains_domain.
const createAuditLogCreatedAtIndex = `
CREATE INDEX IF NOT EXISTS idx_admin_audit_log_created_at ON system.admin_audit_log(created_at DESC)
`

type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// Bootstrap creates system.admin_audit_log (and its index) if it doesn't
// already exist. Idempotent — safe to call on every engine startup, same
// as tenant.Store.Bootstrap.
func (s *Store) Bootstrap(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, createSchema); err != nil {
		return fmt.Errorf("create system schema: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, createAuditLogTable); err != nil {
		return fmt.Errorf("create admin_audit_log table: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, createAuditLogCreatedAtIndex); err != nil {
		return fmt.Errorf("create admin_audit_log created_at index: %w", err)
	}

	return nil
}

// Row is one admin_audit_log entry. OperatorIdentity is never empty — the
// engine writes "internal" for loopback-direct calls rather than leaving
// it blank (engine-internals.md §11). IdempotencyKey and JobID are ""
// when the request didn't send one / didn't return a 202, respectively —
// stored as SQL NULL, not the empty string, so a query distinguishing
// "no idempotency key was sent" from "sent as an empty string" (which the
// admin API itself never accepts, but the column shouldn't quietly assume
// that on its behalf) stays possible.
type Row struct {
	OperatorIdentity string
	Endpoint         string
	TargetScope      string
	IdempotencyKey   string
	JobID            string
	StatusCode       int
}

// Write inserts one row. There is no corresponding Update or Delete —
// see the package doc comment.
func (s *Store) Write(ctx context.Context, row Row) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO system.admin_audit_log
			(operator_identity, endpoint, target_scope, idempotency_key, job_id, status_code)
		VALUES ($1, $2, $3, NULLIF($4, ''), NULLIF($5, ''), $6)
	`, row.OperatorIdentity, row.Endpoint, row.TargetScope, row.IdempotencyKey, row.JobID, row.StatusCode)
	if err != nil {
		return fmt.Errorf("insert admin_audit_log row: %w", err)
	}

	return nil
}
