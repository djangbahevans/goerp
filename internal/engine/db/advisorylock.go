package db

import (
	"context"
	"database/sql"
	"fmt"
	"hash/fnv"
	"slices"
)

// AdvisoryLockKey deterministically derives a Postgres advisory lock key
// from name, the same fnv-hash approach schema.SchemaSyncPool's own
// advisoryLockKeys uses for per-(tenant,module) sync locks — just a
// single int64 key here instead of a 2×int32 pair, since callers of
// WithAdvisoryLock only ever need plain keys, not a composite pair.
func AdvisoryLockKey(name string) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(name))
	return int64(h.Sum64())
}

// SystemSchemaLockKey is the advisory lock key shared by every package
// that issues CREATE SCHEMA IF NOT EXISTS system as part of its own
// Bootstrap (tenant.Store, schema.SchemaSyncPool, auditlog.Store) — see
// EnsureSystemSchema. A per-package key wouldn't protect this statement,
// since it's the same catalog object across all three packages, not a
// per-package-disjoint one the way each package's own tables are.
var SystemSchemaLockKey = AdvisoryLockKey("system-schema")

const createSystemSchema = `CREATE SCHEMA IF NOT EXISTS system`

// EnsureSystemSchema creates the system schema if it doesn't already
// exist, using the given transaction. Callers must already hold
// SystemSchemaLockKey via WithAdvisoryLock before calling this — it does
// not acquire that lock itself.
func EnsureSystemSchema(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, createSystemSchema); err != nil {
		return fmt.Errorf("create system schema: %w", err)
	}
	return nil
}

// WithAdvisoryLock runs fn in a transaction holding the requested advisory locks in sorted
// order to avoid deadlocks. fn must use the supplied transaction; transaction-scoped locks
// remain safe through PgBouncer transaction pooling.
func WithAdvisoryLock(ctx context.Context, pool *sql.DB, keys []int64, fn func(tx *sql.Tx) error) error {
	sorted := slices.Clone(keys)
	slices.Sort(sorted)

	tx, err := pool.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin bootstrap transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	for _, key := range sorted {
		if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock($1)", key); err != nil {
			return fmt.Errorf("acquire advisory lock: %w", err)
		}
	}

	if err := fn(tx); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit bootstrap transaction: %w", err)
	}

	return nil
}
