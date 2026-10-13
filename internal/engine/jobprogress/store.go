package jobprogress

import (
	"context"
	"database/sql"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/db"
)

var ErrNotFound = errors.New("job not found")

type Store struct{ db *sql.DB }

func New(pool *sql.DB) *Store { return &Store{db: pool} }

func (s *Store) Bootstrap(ctx context.Context) error {
	return db.WithAdvisoryLock(ctx, s.db, []int64{db.SystemSchemaLockKey, db.AdvisoryLockKey("jobprogress.Bootstrap")}, func(tx *sql.Tx) error {
		if err := db.EnsureSystemSchema(ctx, tx); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS system.job_progress (
   job_id BIGINT PRIMARY KEY,
   percent SMALLINT NOT NULL DEFAULT 0,
   message TEXT NOT NULL DEFAULT '',
   result JSONB,
   updated_at TIMESTAMPTZ NOT NULL
  )`)
		if err != nil {
			return fmt.Errorf("create job progress table: %w", err)
		}
		return nil
	})
}

func (s *Store) Begin(id int64) Recorder {
	return NewAttempt(func(ctx context.Context, report Report) error { return s.Save(ctx, id, report) })
}

func (s *Store) Save(ctx context.Context, id int64, report Report) error {
	var result any
	if len(report.Result) > 0 {
		result = string(report.Result)
	}

	_, err := s.db.ExecContext(ctx, `INSERT INTO system.job_progress (job_id, percent, message, result, updated_at)
 VALUES ($1,$2,$3,$4,NOW()) ON CONFLICT (job_id) DO UPDATE SET
 percent = CASE WHEN $5 THEN EXCLUDED.percent ELSE job_progress.percent END,
 message = CASE WHEN $5 THEN EXCLUDED.message ELSE job_progress.message END,
 result = EXCLUDED.result, updated_at = EXCLUDED.updated_at`, id, report.Percent, report.Message, result, report.HasProgress)
	if err != nil {
		return fmt.Errorf("save job progress: %w", err)
	}
	return nil
}

type Failure struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type Snapshot struct {
	JobID       string         `json:"job_id"`
	Status      string         `json:"status"`
	Percent     int            `json:"percent"`
	Message     string         `json:"message"`
	Result      jsontext.Value `json:"result,omitzero"`
	Error       *Failure       `json:"error,omitzero"`
	StartedAt   *time.Time     `json:"started_at"`
	CompletedAt *time.Time     `json:"completed_at"`
}

// Read applies ownership before loading progress, including jobs that have no reports.
func (s *Store) Read(ctx context.Context, id int64, tenant, user string) (Snapshot, error) {
	var snapshot Snapshot
	var state string
	var result []byte
	var started, completed sql.NullTime

	err := s.db.QueryRowContext(ctx, `SELECT j.state, COALESCE(p.percent,0), COALESCE(p.message,''), p.result,
 LEAST(j.attempted_at, (j.errors[1]->>'at')::timestamptz), j.finalized_at FROM system.river_job j LEFT JOIN system.job_progress p ON p.job_id=j.id
 WHERE j.id=$1 AND j.kind='wasm_job' AND j.args->>'tenant_id'=$2 AND j.args->>'user_id'=$3
 AND $3<>'' AND COALESCE((j.args->>'is_data_migration')::boolean,false)=false
 AND COALESCE((j.args->>'is_cron')::boolean,false)=false
 AND COALESCE(j.args->>'notification_id','')=''`, id, tenant, user).Scan(&state, &snapshot.Percent, &snapshot.Message, &result, &started, &completed)
	if errors.Is(err, sql.ErrNoRows) {
		return Snapshot{}, ErrNotFound
	}
	if err != nil {
		return Snapshot{}, fmt.Errorf("read job progress: %w", err)
	}

	switch state {
	case "available", "scheduled", "pending":
		snapshot.Status = "pending"
	case "running", "retryable":
		snapshot.Status = "running"
	case "completed":
		snapshot.Status, snapshot.Percent, snapshot.Result = "complete", 100, result
	case "cancelled":
		snapshot.Status, snapshot.Error = "failed", &Failure{Code: "jobs.cancelled"}
	case "discarded":
		snapshot.Status, snapshot.Error = "failed", &Failure{Code: "jobs.failed"}
	default:
		return Snapshot{}, fmt.Errorf("unknown job state %q", state)
	}

	if started.Valid && snapshot.Status != "pending" {
		snapshot.StartedAt = new(started.Time)
	}
	if completed.Valid && (snapshot.Status == "complete" || snapshot.Status == "failed") {
		snapshot.CompletedAt = new(completed.Time)
	}
	return snapshot, nil
}

func (s *Store) Prune(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM system.job_progress p WHERE NOT EXISTS (SELECT 1 FROM system.river_job j WHERE j.id=p.job_id)`)
	if err != nil {
		return fmt.Errorf("prune job progress: %w", err)
	}
	return nil
}
