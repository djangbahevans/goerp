package jobqueue

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
)

// ReindexArgs is the daily job that rebuilds River's indexes; River's own
// reindexer runs as the client's role, which doesn't own them.
type ReindexArgs struct{}

func (ReindexArgs) Kind() string { return "job_queue_reindex" }

func (ReindexArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: QueueAdmin}
}

// ReindexWorker runs REINDEX CONCURRENTLY on River's default index set.
// Pool is the schema-sync pool, whose role owns River's tables.
type ReindexWorker struct {
	river.WorkerDefaults[ReindexArgs]
	Pool *sql.DB
}

// invalidReindexArtifactsQuery finds indexes an interrupted REINDEX
// CONCURRENTLY left behind ("<name>_ccnew", "<name>_ccold1", ...).
const invalidReindexArtifactsQuery = `
SELECT c.relname
FROM pg_index i
JOIN pg_class c ON c.oid = i.indexrelid
WHERE c.relnamespace = $1::regnamespace
  AND NOT i.indisvalid
  AND c.relname ~ ('^' || $2 || '_cc(new|old)[0-9]*$')`

func (w *ReindexWorker) Work(ctx context.Context, job *river.Job[ReindexArgs]) error {
	for _, name := range river.ReindexerIndexNamesDefault() {
		if err := w.dropInvalidArtifacts(ctx, name); err != nil {
			return err
		}
		var exists bool
		if err := w.Pool.QueryRowContext(ctx, `SELECT to_regclass($1) IS NOT NULL`, Schema+"."+name).Scan(&exists); err != nil {
			return fmt.Errorf("check index %s: %w", name, err)
		}
		if !exists {
			continue
		}
		if _, err := w.Pool.ExecContext(ctx, "REINDEX INDEX CONCURRENTLY "+pgx.Identifier{Schema, name}.Sanitize()); err != nil {
			return fmt.Errorf("reindex %s: %w", name, err)
		}
	}
	return nil
}

func (w *ReindexWorker) dropInvalidArtifacts(ctx context.Context, name string) error {
	rows, err := w.Pool.QueryContext(ctx, invalidReindexArtifactsQuery, Schema, name)
	if err != nil {
		return fmt.Errorf("find invalid reindex artifacts of %s: %w", name, err)
	}
	var artifacts []string
	for rows.Next() {
		var a string
		if err := rows.Scan(&a); err != nil {
			_ = rows.Close()
			return fmt.Errorf("scan reindex artifact: %w", err)
		}
		artifacts = append(artifacts, a)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("find invalid reindex artifacts of %s: %w", name, err)
	}
	for _, a := range artifacts {
		if _, err := w.Pool.ExecContext(ctx, "DROP INDEX CONCURRENTLY IF EXISTS "+pgx.Identifier{Schema, a}.Sanitize()); err != nil {
			return fmt.Errorf("drop reindex artifact %s: %w", a, err)
		}
	}
	return nil
}
