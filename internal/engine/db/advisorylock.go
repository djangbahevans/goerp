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

// WithAdvisoryLock runs fn inside one Postgres transaction holding a
// transaction-scoped advisory lock (pg_advisory_xact_lock) for every key
// in keys, so concurrent first-time Bootstrap callers targeting the same
// key(s) serialize instead of racing (goerp#171: two callers can both
// pass CREATE TABLE IF NOT EXISTS's existence check before either
// commits, then collide on actually creating it). fn must issue every
// statement through the *sql.Tx it receives, never through pool directly
// — anything issued through pool runs on a different, unlocked
// connection and defeats both the mutual exclusion and this call's
// atomicity.
//
// A transaction-scoped lock, not a session-scoped pg_advisory_lock
// released via a separate pg_advisory_unlock call, is required here
// specifically because pool may be PgBouncer-fronted in transaction
// pooling mode (compose.dev.yml's POOL_MODE: transaction, data-layer.md
// "PgBouncer configuration"): a session-level lock's acquire and its
// later release could land on two different backend connections under
// transaction pooling, which wouldn't just make the lock ineffective but
// could leave it stuck against a backend that never actually held it. A
// transaction-scoped lock only needs the guarantee PgBouncer's own
// transaction-pooling contract already provides — one transaction stays
// on one backend connection for its duration — and Postgres releases the
// lock automatically at commit or rollback, so there's no separate
// unlock call to get wrong. This also works unchanged for a pool that
// bypasses PgBouncer entirely, so every caller can use the same helper
// regardless of which kind of pool it was given.
//
// keys is sorted ascending before acquisition, so two Bootstrap callers
// that both need e.g. {SystemSchemaLockKey, their own key} always
// request them in the same order — ruling out a lock-ordering deadlock
// between them by construction.
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
