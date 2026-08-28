// Package checkpoint provides a generic per-(job, module) checkpoint/lease
// mechanism for resumable engine-owned River jobs — goerp tenant
// export/import's own resumability requirement (cli-reference.md §5), the
// same shape of problem analytics-guide.md §12's
// analytics_projection_backfill mechanism solves for ClickHouse backfills,
// generalized here without that mechanism's ClickHouse-specific
// schema_version column or concurrent-live-sync version resolution
// (neither applies outside ClickHouse).
//
// A River job that fails and retries re-invokes its handler from the
// top — nothing in River itself remembers how far a previous attempt
// got. Store persists a high-water mark (LastID) per (job, module) so a
// retried job resumes instead of re-walking already-processed work, plus
// a lease (owner + heartbeat) so two concurrent attempts at the same
// (job, module) don't duplicate work.
package checkpoint

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/db"
)

type Status string

const (
	StatusPending  Status = "pending"
	StatusRunning  Status = "running"
	StatusComplete Status = "complete"
	StatusFailed   Status = "failed"
)

const createJobCheckpointsTable = `
CREATE TABLE IF NOT EXISTS system.job_checkpoints (
    job_id          TEXT NOT NULL,
    module          TEXT NOT NULL,
    tenant_id       UUID NOT NULL,
    last_id         TEXT NOT NULL DEFAULT '',
    status          TEXT NOT NULL DEFAULT 'pending'
                        CHECK (status IN ('pending', 'running', 'complete', 'failed')),
    lease_owner     TEXT,
    lease_heartbeat TIMESTAMPTZ,
    started_at      TIMESTAMPTZ,
    completed_at    TIMESTAMPTZ,
    PRIMARY KEY (job_id, module)
)
`

type Store struct {
	db *sql.DB
}

func NewStore(conn *sql.DB) *Store {
	return &Store{db: conn}
}

// Bootstrap creates system.job_checkpoints if it doesn't already exist.
// Idempotent and concurrent-safe against other processes calling
// Bootstrap at the same time, same convention billing.Store.Bootstrap
// uses.
func (s *Store) Bootstrap(ctx context.Context) error {
	keys := []int64{db.SystemSchemaLockKey, db.AdvisoryLockKey("checkpoint.Bootstrap")}
	return db.WithAdvisoryLock(ctx, s.db, keys, func(tx *sql.Tx) error {
		if err := db.EnsureSystemSchema(ctx, tx); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, createJobCheckpointsTable); err != nil {
			return fmt.Errorf("create job_checkpoints table: %w", err)
		}
		return nil
	})
}

// ErrLeaseHeld is returned by AcquireLease when a live (non-stale) lease
// is already held by a different owner.
var ErrLeaseHeld = errors.New("checkpoint: lease already held by a live runner")

// ErrAlreadyComplete is returned by AcquireLease when (jobID, module) has
// already reached StatusComplete.
var ErrAlreadyComplete = errors.New("checkpoint: already complete")

// Progress is the handle AcquireLease returns — LastID is the high-water
// mark to resume from, and AdvanceCheckpoint/MarkFailed/MarkComplete are
// the only ways to mutate this (job, module)'s row from here on, mirroring
// analytics-guide.md §12's own AcquireBackfillLease/progress ergonomics.
type Progress struct {
	store  *Store
	jobID  string
	module string
	LastID string
}

// AcquireLease acquires (or resumes/reclaims) the lease for (jobID,
// module), creating its row first if this is the first attempt.
// Succeeds when the row is StatusPending/StatusFailed, or StatusRunning
// with a lease_heartbeat older than staleAfter (the previous runner is
// presumed dead and the lease is reclaimed). Returns ErrLeaseHeld against
// a live concurrent lease, ErrAlreadyComplete against a StatusComplete
// row.
func (s *Store) AcquireLease(ctx context.Context, jobID, module, tenantID, leaseOwner string, staleAfter time.Duration) (*Progress, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO system.job_checkpoints (job_id, module, tenant_id)
		VALUES ($1, $2, $3)
		ON CONFLICT (job_id, module) DO NOTHING
	`, jobID, module, tenantID); err != nil {
		return nil, fmt.Errorf("ensure checkpoint row: %w", err)
	}

	staleBefore := time.Now().Add(-staleAfter)
	var lastID string
	err = tx.QueryRowContext(ctx, `
		UPDATE system.job_checkpoints
		SET status = 'running', lease_owner = $4, lease_heartbeat = NOW(),
		    started_at = COALESCE(started_at, NOW())
		WHERE job_id = $1 AND module = $2
		  AND (status IN ('pending', 'failed') OR (status = 'running' AND lease_heartbeat < $3))
		RETURNING last_id
	`, jobID, module, staleBefore, leaseOwner).Scan(&lastID)
	if err == nil {
		if err := tx.Commit(); err != nil {
			return nil, fmt.Errorf("commit lease acquisition: %w", err)
		}
		return &Progress{store: s, jobID: jobID, module: module, LastID: lastID}, nil
	}
	if err != sql.ErrNoRows {
		return nil, fmt.Errorf("acquire lease: %w", err)
	}

	// The UPDATE matched no row — either it's complete, or a different
	// owner holds a still-live lease. Distinguish the two for a clear
	// error, same transaction so the read is consistent with the failed
	// UPDATE above.
	var status Status
	if err := tx.QueryRowContext(ctx, `SELECT status FROM system.job_checkpoints WHERE job_id = $1 AND module = $2`, jobID, module).Scan(&status); err != nil {
		return nil, fmt.Errorf("check checkpoint status: %w", err)
	}
	if status == StatusComplete {
		return nil, ErrAlreadyComplete
	}
	return nil, ErrLeaseHeld
}

// AdvanceCheckpoint bumps last_id after a successfully-processed batch.
func (p *Progress) AdvanceCheckpoint(ctx context.Context, lastID string) error {
	if _, err := p.store.db.ExecContext(ctx, `
		UPDATE system.job_checkpoints SET last_id = $3, lease_heartbeat = NOW()
		WHERE job_id = $1 AND module = $2
	`, p.jobID, p.module, lastID); err != nil {
		return fmt.Errorf("advance checkpoint: %w", err)
	}
	p.LastID = lastID
	return nil
}

// MarkFailed sets status = 'failed' — a subsequent AcquireLease call can
// retry from the last-advanced checkpoint.
func (p *Progress) MarkFailed(ctx context.Context) error {
	if _, err := p.store.db.ExecContext(ctx, `
		UPDATE system.job_checkpoints SET status = 'failed', lease_owner = NULL
		WHERE job_id = $1 AND module = $2
	`, p.jobID, p.module); err != nil {
		return fmt.Errorf("mark checkpoint failed: %w", err)
	}
	return nil
}

// MarkComplete sets status = 'complete' and completed_at — terminal; a
// subsequent AcquireLease call returns ErrAlreadyComplete.
func (p *Progress) MarkComplete(ctx context.Context) error {
	if _, err := p.store.db.ExecContext(ctx, `
		UPDATE system.job_checkpoints SET status = 'complete', lease_owner = NULL, completed_at = NOW()
		WHERE job_id = $1 AND module = $2
	`, p.jobID, p.module); err != nil {
		return fmt.Errorf("mark checkpoint complete: %w", err)
	}
	return nil
}
