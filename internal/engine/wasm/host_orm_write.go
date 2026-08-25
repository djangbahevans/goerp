package wasm

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/djangbahevans/goerp/internal/engine/cache"
	"github.com/djangbahevans/goerp/internal/engine/orm"
	"github.com/djangbahevans/goerp/sdk/go/model"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/riverqueue/river"
	"github.com/tetratelabs/wazero/api"
	"github.com/vmihailenco/msgpack/v5"
)

// This file holds host.orm's write half — create/create_batch/
// first_or_create/write/write_many/write_where/unlink (goerp#343/#380),
// Store(true)/.Depends() computed-field recompute (goerp#377) for
// Table-backed models (recomputeAfterWrite runs inside the same
// transaction as the triggering create/write, for both same-record and
// one-hop Many2One dependencies), and orm.RegisterConstraint hooks
// (goerp#378, runConstraintHook — host_orm_constraint.go), which run
// after recomputeAfterWrite so a hook sees the fully-recomputed row, and
// before event emission so a rejection aborts the whole transaction.
// DynamicLink/.Tree() validation (no such field kinds exist yet),
// One2Many child-triggered recompute (goerp#388), and audit logging
// (goerp#363, tracked separately — no audited_tables trigger mechanism
// exists anywhere in the engine despite this ticket's own original text
// assuming one did) are all explicitly out of scope for this file — see
// goerp#379/#383/#388.
//
// create_batch/first_or_create/write_many/write_where are not supported
// for Transient-backed models (a Redis-backed key has no domain-query or
// multi-row-transaction story the way a Postgres table does) — each
// returns a descriptive error rather than silently misbehaving. Computed
// fields on a Transient model are likewise out of scope here — recompute
// is wired only for the Postgres-backed create/write cores below.

type OnConflictOption struct {
	Fields []string `msgpack:"fields"`
	Policy string   `msgpack:"policy"` // "ignore" | "update"
}

type ORMCreateInput struct {
	Model      string            `msgpack:"model"`
	Record     map[string]any    `msgpack:"record"`
	OnConflict *OnConflictOption `msgpack:"on_conflict,omitempty"`
}

type ORMCreateOutput struct {
	Record map[string]any `msgpack:"record"`
}

type ORMCreateBatchInput struct {
	Model      string            `msgpack:"model"`
	Records    []map[string]any  `msgpack:"records"`
	OnConflict *OnConflictOption `msgpack:"on_conflict,omitempty"`
}

type ORMCreateBatchOutput struct {
	Records []map[string]any `msgpack:"records"`
}

type ORMFirstOrCreateInput struct {
	Model  string         `msgpack:"model"`
	Domain string         `msgpack:"domain"`
	Record map[string]any `msgpack:"record"`
}

type ORMFirstOrCreateOutput struct {
	Record  map[string]any `msgpack:"record"`
	Created bool           `msgpack:"created"`
}

type ORMWriteInput struct {
	Model        string         `msgpack:"model"`
	ID           string         `msgpack:"id"`
	Record       map[string]any `msgpack:"record"`
	ExpectedEtag string         `msgpack:"expected_etag,omitempty"`
}

type ORMWriteOutput struct {
	Record map[string]any `msgpack:"record"`
}

type ORMWriteManyInput struct {
	Model  string         `msgpack:"model"`
	IDs    []string       `msgpack:"ids"`
	Record map[string]any `msgpack:"record"`
}

type ORMWriteWhereInput struct {
	Model  string         `msgpack:"model"`
	Domain string         `msgpack:"domain"`
	Record map[string]any `msgpack:"record"`
}

// ExecResult is write_many/write_where's return shape — how many rows
// changed and which ones, without the cost of returning every full record
// body for a call that could touch many rows. host-abi-reference.md names
// this type but never defines its fields; nothing else in the repo
// declares it, so this is the shape it gets.
type ExecResult struct {
	Count int      `msgpack:"count"`
	IDs   []string `msgpack:"ids"`
}

type ORMUnlinkInput struct {
	Model string `msgpack:"model"`
	ID    string `msgpack:"id"`
}

type ORMUnlinkOutput struct {
	Deleted bool `msgpack:"deleted"`
}

func makeORMCreate(r *Runtime, db *sql.DB, insertClient *river.Client[*sql.Tx], cacheClient *cache.Client) func(ctx context.Context, m api.Module, ptr, length uint32) uint64 {
	return func(ctx context.Context, m api.Module, ptr, length uint32) uint64 {
		inst := r.InstanceForModule(m)
		modCtx := inst.ModuleContext()
		allocate := inst.allocate

		inputBytes, err := abi.ReadFromModule(m.Memory(), ptr, length)
		if err != nil {
			return abi.EncodeHostError(ctx, m, allocate, abi.MemoryFault())
		}
		var input ORMCreateInput
		if err := msgpack.Unmarshal(inputBytes, &input); err != nil {
			return abi.EncodeHostError(ctx, m, allocate, abi.DeserializeError(err))
		}

		out, hostErr := ORMCreate(ctx, r, db, insertClient, cacheClient, modCtx, input)
		if hostErr != nil {
			return abi.EncodeHostError(ctx, m, allocate, hostErr)
		}
		return abi.WriteToModule(ctx, m, allocate, out)
	}
}

// ORMCreate is host.orm create's plain-Go core — see ORMSearch's doc
// comment (host_orm.go) for the shared-entry-point rationale. Branches to
// transientCreate (host_orm_transient.go) for Transient-backed models
// internally. The single-row insert itself is createOneRecordTx, shared
// with ORMCreateBatch/ORMFirstOrCreate below. r is only used for
// recomputeAfterWrite's cross-module compute dispatch — every other
// dependency stays a plain arg, matching this file's existing style.
func ORMCreate(ctx context.Context, r *Runtime, db *sql.DB, insertClient *river.Client[*sql.Tx], cacheClient *cache.Client, modCtx *ModuleContext, input ORMCreateInput) (ORMCreateOutput, *abi.HostError) {
	if !modCtx.Capabilities().Has(abi.CapDBWrite) {
		return ORMCreateOutput{}, abi.CapabilityDenied("db.write")
	}

	md, ok := resolveModel(modCtx, input.Model)
	if !ok {
		return ORMCreateOutput{}, &abi.HostError{Code: abi.ErrCodeModelNotFound, Message: "model " + input.Model + " is not declared by this module"}
	}

	record := make(map[string]any, len(input.Record))
	maps.Copy(record, input.Record)

	if hostErr := validateRequired(md, record, true); hostErr != nil {
		return ORMCreateOutput{}, hostErr
	}

	if md.Backend == model.BackendTransient {
		return transientCreate(ctx, cacheClient, modCtx, md, input.Model, record)
	}

	tx, err := beginTenantScopedWrite(ctx, db, modCtx)
	if err != nil {
		return ORMCreateOutput{}, &abi.HostError{Code: abi.ErrCodeUnavailable, Message: err.Error(), Retry: true}
	}
	defer func() { _ = tx.Rollback() }()

	if hostErr := acquireSequenceFields(ctx, tx, modCtx.TenantSlug, input.Model, md, record); hostErr != nil {
		return ORMCreateOutput{}, hostErr
	}

	row, inserted, hostErr := createOneRecordTx(ctx, tx, md, input.Model, record, input.OnConflict)
	if hostErr != nil {
		return ORMCreateOutput{}, hostErr
	}
	if row == nil {
		// OnConflictIgnore skipped this row — nothing was created, so
		// there's nothing to return and no event to emit. Matches the
		// doc's own framing: a successful create call looks identical
		// whether it inserted or hit the conflict target.
		if err := tx.Commit(); err != nil {
			return ORMCreateOutput{}, &abi.HostError{Code: abi.ErrCodeCommitFailed, Message: err.Error()}
		}
		return ORMCreateOutput{}, nil
	}

	if hostErr := recomputeAfterWrite(ctx, tx, r, modCtx, input.Model, md, changedFieldNames(row), row); hostErr != nil {
		return ORMCreateOutput{}, hostErr
	}
	if hostErr := runConstraintHook(ctx, r, modCtx, input.Model, "create", row); hostErr != nil {
		return ORMCreateOutput{}, hostErr
	}

	eventName := "orm.record.created"
	if !inserted {
		eventName = "orm.record.updated"
	}
	if err := emitRecordEvent(ctx, insertClient, tx, modCtx, eventName, input.Model, row); err != nil {
		return ORMCreateOutput{}, &abi.HostError{Code: abi.ErrCodeUnavailable, Message: err.Error(), Retry: true}
	}

	if err := tx.Commit(); err != nil {
		return ORMCreateOutput{}, &abi.HostError{Code: abi.ErrCodeCommitFailed, Message: err.Error()}
	}

	return ORMCreateOutput{Record: row}, nil
}

func makeORMCreateBatch(r *Runtime, db *sql.DB, insertClient *river.Client[*sql.Tx]) func(ctx context.Context, m api.Module, ptr, length uint32) uint64 {
	return func(ctx context.Context, m api.Module, ptr, length uint32) uint64 {
		inst := r.InstanceForModule(m)
		modCtx := inst.ModuleContext()
		allocate := inst.allocate

		inputBytes, err := abi.ReadFromModule(m.Memory(), ptr, length)
		if err != nil {
			return abi.EncodeHostError(ctx, m, allocate, abi.MemoryFault())
		}
		var input ORMCreateBatchInput
		if err := msgpack.Unmarshal(inputBytes, &input); err != nil {
			return abi.EncodeHostError(ctx, m, allocate, abi.DeserializeError(err))
		}

		out, hostErr := ORMCreateBatch(ctx, r, db, insertClient, modCtx, input)
		if hostErr != nil {
			return abi.EncodeHostError(ctx, m, allocate, hostErr)
		}
		return abi.WriteToModule(ctx, m, allocate, out)
	}
}

// ORMCreateBatch inserts every record in input.Records inside one
// transaction — sequential single-row INSERTs sharing one tx, not a true
// multi-row VALUES/COPY statement: the doc's "COPY-backed" phrasing is an
// SDK-level performance aspiration, not a correctness requirement, and
// sequential-in-one-tx satisfies the real AC ("all-or-nothing, one
// failure aborts the whole batch") via ordinary rollback while trivially
// handling records with different field sets, which a single multi-row
// statement would need a uniform column list for. Emits at most two
// batched events (not one per record) — orm.record.created listing every
// genuinely-inserted record, orm.record.updated listing any
// OnConflictUpdate rows — matching the doc's explicit "batched into one
// event with all IDs for create_batch, not one event per row".
func ORMCreateBatch(ctx context.Context, r *Runtime, db *sql.DB, insertClient *river.Client[*sql.Tx], modCtx *ModuleContext, input ORMCreateBatchInput) (ORMCreateBatchOutput, *abi.HostError) {
	if !modCtx.Capabilities().Has(abi.CapDBWrite) {
		return ORMCreateBatchOutput{}, abi.CapabilityDenied("db.write")
	}

	md, ok := resolveModel(modCtx, input.Model)
	if !ok {
		return ORMCreateBatchOutput{}, &abi.HostError{Code: abi.ErrCodeModelNotFound, Message: "model " + input.Model + " is not declared by this module"}
	}
	if md.Backend == model.BackendTransient {
		return ORMCreateBatchOutput{}, &abi.HostError{Code: abi.ErrCodeUnavailable, Message: "create_batch is not supported for Transient-backed models"}
	}
	if len(input.Records) == 0 {
		return ORMCreateBatchOutput{}, &abi.HostError{Code: abi.ErrCodeValidationFailed, Message: "records must not be empty"}
	}

	tx, err := beginTenantScopedWrite(ctx, db, modCtx)
	if err != nil {
		return ORMCreateBatchOutput{}, &abi.HostError{Code: abi.ErrCodeUnavailable, Message: err.Error(), Retry: true}
	}
	defer func() { _ = tx.Rollback() }()

	var all, createdForEvent, updatedForEvent []map[string]any
	for _, rec := range input.Records {
		record := make(map[string]any, len(rec))
		maps.Copy(record, rec)

		if hostErr := validateRequired(md, record, true); hostErr != nil {
			return ORMCreateBatchOutput{}, hostErr
		}
		if hostErr := acquireSequenceFields(ctx, tx, modCtx.TenantSlug, input.Model, md, record); hostErr != nil {
			return ORMCreateBatchOutput{}, hostErr
		}

		row, inserted, hostErr := createOneRecordTx(ctx, tx, md, input.Model, record, input.OnConflict)
		if hostErr != nil {
			return ORMCreateBatchOutput{}, hostErr
		}
		if row == nil {
			continue // OnConflictIgnore skipped this one
		}
		if hostErr := recomputeAfterWrite(ctx, tx, r, modCtx, input.Model, md, changedFieldNames(row), row); hostErr != nil {
			return ORMCreateBatchOutput{}, hostErr
		}
		if hostErr := runConstraintHook(ctx, r, modCtx, input.Model, "create", row); hostErr != nil {
			return ORMCreateBatchOutput{}, hostErr
		}
		all = append(all, row)
		if inserted {
			createdForEvent = append(createdForEvent, row)
		} else {
			updatedForEvent = append(updatedForEvent, row)
		}
	}

	if len(createdForEvent) > 0 {
		if err := emitBatchRecordEvent(ctx, insertClient, tx, modCtx, "orm.record.created", input.Model, createdForEvent); err != nil {
			return ORMCreateBatchOutput{}, &abi.HostError{Code: abi.ErrCodeUnavailable, Message: err.Error(), Retry: true}
		}
	}
	if len(updatedForEvent) > 0 {
		if err := emitBatchRecordEvent(ctx, insertClient, tx, modCtx, "orm.record.updated", input.Model, updatedForEvent); err != nil {
			return ORMCreateBatchOutput{}, &abi.HostError{Code: abi.ErrCodeUnavailable, Message: err.Error(), Retry: true}
		}
	}

	if err := tx.Commit(); err != nil {
		return ORMCreateBatchOutput{}, &abi.HostError{Code: abi.ErrCodeCommitFailed, Message: err.Error()}
	}

	return ORMCreateBatchOutput{Records: all}, nil
}

func makeORMFirstOrCreate(r *Runtime, db *sql.DB, insertClient *river.Client[*sql.Tx]) func(ctx context.Context, m api.Module, ptr, length uint32) uint64 {
	return func(ctx context.Context, m api.Module, ptr, length uint32) uint64 {
		inst := r.InstanceForModule(m)
		modCtx := inst.ModuleContext()
		allocate := inst.allocate

		inputBytes, err := abi.ReadFromModule(m.Memory(), ptr, length)
		if err != nil {
			return abi.EncodeHostError(ctx, m, allocate, abi.MemoryFault())
		}
		var input ORMFirstOrCreateInput
		if err := msgpack.Unmarshal(inputBytes, &input); err != nil {
			return abi.EncodeHostError(ctx, m, allocate, abi.DeserializeError(err))
		}

		out, hostErr := ORMFirstOrCreate(ctx, r, db, insertClient, modCtx, input)
		if hostErr != nil {
			return abi.EncodeHostError(ctx, m, allocate, hostErr)
		}
		return abi.WriteToModule(ctx, m, allocate, out)
	}
}

// ORMFirstOrCreate searches input.Domain and creates input.Record only on
// a miss, inside one transaction. Race-safety for concurrent callers
// racing an *arbitrary* domain (unlike OnConflict's target, a domain
// isn't necessarily backed by a unique index) uses a transaction-scoped
// Postgres advisory lock keyed by hash(tenant, model, domain) — the
// general-condition counterpart to AcquireNext's (internal/engine/orm)
// keyed INSERT...ON CONFLICT DO UPDATE upsert pattern. The lock only
// serializes callers racing the identical (tenant, model, domain) triple;
// it's released automatically at commit/rollback.
func ORMFirstOrCreate(ctx context.Context, r *Runtime, db *sql.DB, insertClient *river.Client[*sql.Tx], modCtx *ModuleContext, input ORMFirstOrCreateInput) (ORMFirstOrCreateOutput, *abi.HostError) {
	if !modCtx.Capabilities().Has(abi.CapDBWrite) {
		return ORMFirstOrCreateOutput{}, abi.CapabilityDenied("db.write")
	}

	md, ok := resolveModel(modCtx, input.Model)
	if !ok {
		return ORMFirstOrCreateOutput{}, &abi.HostError{Code: abi.ErrCodeModelNotFound, Message: "model " + input.Model + " is not declared by this module"}
	}
	if md.Backend == model.BackendTransient {
		return ORMFirstOrCreateOutput{}, &abi.HostError{Code: abi.ErrCodeUnavailable, Message: "first_or_create is not supported for Transient-backed models"}
	}

	whereFrag, whereArgs, hostErr := compileDomain(input.Domain)
	if hostErr != nil {
		return ORMFirstOrCreateOutput{}, hostErr
	}

	tx, err := beginTenantScopedWrite(ctx, db, modCtx)
	if err != nil {
		return ORMFirstOrCreateOutput{}, &abi.HostError{Code: abi.ErrCodeUnavailable, Message: err.Error(), Retry: true}
	}
	defer func() { _ = tx.Rollback() }()

	lockKey := modCtx.TenantSlug + ":" + input.Model + ":" + input.Domain
	if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1, 0))", lockKey); err != nil {
		return ORMFirstOrCreateOutput{}, &abi.HostError{Code: abi.ErrCodeUnavailable, Message: err.Error(), Retry: true}
	}

	table := quoteIdentORM(tableNameForORM(md))
	rows, err := tx.QueryContext(ctx, fmt.Sprintf("SELECT * FROM %s WHERE %s LIMIT 1", table, whereFrag), whereArgs...)
	if err != nil {
		return ORMFirstOrCreateOutput{}, &abi.HostError{Code: abi.ErrCodeUnavailable, Message: err.Error()}
	}
	found, err := scanRowsToMaps(rows)
	if err != nil {
		return ORMFirstOrCreateOutput{}, &abi.HostError{Code: abi.ErrCodeUnavailable, Message: err.Error()}
	}

	if len(found) > 0 {
		if err := tx.Commit(); err != nil {
			return ORMFirstOrCreateOutput{}, &abi.HostError{Code: abi.ErrCodeCommitFailed, Message: err.Error()}
		}
		return ORMFirstOrCreateOutput{Record: found[0], Created: false}, nil
	}

	record := make(map[string]any, len(input.Record))
	maps.Copy(record, input.Record)
	if hostErr := validateRequired(md, record, true); hostErr != nil {
		return ORMFirstOrCreateOutput{}, hostErr
	}
	if hostErr := acquireSequenceFields(ctx, tx, modCtx.TenantSlug, input.Model, md, record); hostErr != nil {
		return ORMFirstOrCreateOutput{}, hostErr
	}

	row, _, hostErr := createOneRecordTx(ctx, tx, md, input.Model, record, nil)
	if hostErr != nil {
		return ORMFirstOrCreateOutput{}, hostErr
	}

	if hostErr := recomputeAfterWrite(ctx, tx, r, modCtx, input.Model, md, changedFieldNames(row), row); hostErr != nil {
		return ORMFirstOrCreateOutput{}, hostErr
	}
	if hostErr := runConstraintHook(ctx, r, modCtx, input.Model, "create", row); hostErr != nil {
		return ORMFirstOrCreateOutput{}, hostErr
	}

	if err := emitRecordEvent(ctx, insertClient, tx, modCtx, "orm.record.created", input.Model, row); err != nil {
		return ORMFirstOrCreateOutput{}, &abi.HostError{Code: abi.ErrCodeUnavailable, Message: err.Error(), Retry: true}
	}
	if err := tx.Commit(); err != nil {
		return ORMFirstOrCreateOutput{}, &abi.HostError{Code: abi.ErrCodeCommitFailed, Message: err.Error()}
	}

	return ORMFirstOrCreateOutput{Record: row, Created: true}, nil
}

func makeORMWrite(r *Runtime, db *sql.DB, insertClient *river.Client[*sql.Tx], cacheClient *cache.Client) func(ctx context.Context, m api.Module, ptr, length uint32) uint64 {
	return func(ctx context.Context, m api.Module, ptr, length uint32) uint64 {
		inst := r.InstanceForModule(m)
		modCtx := inst.ModuleContext()
		allocate := inst.allocate

		inputBytes, err := abi.ReadFromModule(m.Memory(), ptr, length)
		if err != nil {
			return abi.EncodeHostError(ctx, m, allocate, abi.MemoryFault())
		}
		var input ORMWriteInput
		if err := msgpack.Unmarshal(inputBytes, &input); err != nil {
			return abi.EncodeHostError(ctx, m, allocate, abi.DeserializeError(err))
		}

		out, hostErr := ORMWrite(ctx, r, db, insertClient, cacheClient, modCtx, input)
		if hostErr != nil {
			return abi.EncodeHostError(ctx, m, allocate, hostErr)
		}
		return abi.WriteToModule(ctx, m, allocate, out)
	}
}

// ORMWrite is host.orm write's plain-Go core — see ORMSearch's doc
// comment (host_orm.go) for the shared-entry-point rationale. The etag
// rotation happens before the Transient/Table branch deliberately — both
// backends get the same new-etag-on-every-write semantics uniformly.
// Branches to transientWrite (host_orm_transient.go) for Transient-backed
// models internally. The single-row update itself is writeOneRecordTx,
// shared with ORMWriteMany/ORMWriteWhere below.
func ORMWrite(ctx context.Context, r *Runtime, db *sql.DB, insertClient *river.Client[*sql.Tx], cacheClient *cache.Client, modCtx *ModuleContext, input ORMWriteInput) (ORMWriteOutput, *abi.HostError) {
	if !modCtx.Capabilities().Has(abi.CapDBWrite) {
		return ORMWriteOutput{}, abi.CapabilityDenied("db.write")
	}

	md, ok := resolveModel(modCtx, input.Model)
	if !ok {
		return ORMWriteOutput{}, &abi.HostError{Code: abi.ErrCodeModelNotFound, Message: "model " + input.Model + " is not declared by this module"}
	}

	if hostErr := validateRequired(md, input.Record, false); hostErr != nil {
		return ORMWriteOutput{}, hostErr
	}

	record := make(map[string]any, len(input.Record)+1)
	maps.Copy(record, input.Record)
	newEtag := uuid.Must(uuid.NewV7()).String()
	if hasField(md, "etag") {
		record["etag"] = newEtag
	}

	if md.Backend == model.BackendTransient {
		return transientWrite(ctx, cacheClient, modCtx, md, input.Model, input.ID, record, newEtag, input.ExpectedEtag)
	}

	pkCol, ok := primaryKeyColumn(md)
	if !ok {
		return ORMWriteOutput{}, &abi.HostError{Code: abi.ErrCodeModelNotFound, Message: "model " + input.Model + " declares no primary key field"}
	}

	tx, err := beginTenantScopedWrite(ctx, db, modCtx)
	if err != nil {
		return ORMWriteOutput{}, &abi.HostError{Code: abi.ErrCodeUnavailable, Message: err.Error(), Retry: true}
	}
	defer func() { _ = tx.Rollback() }()

	updated, hostErr := writeOneRecordTx(ctx, tx, md, pkCol, input.ID, record, input.ExpectedEtag)
	if hostErr != nil {
		return ORMWriteOutput{}, hostErr
	}

	if hostErr := recomputeAfterWrite(ctx, tx, r, modCtx, input.Model, md, changedFieldNames(input.Record), updated); hostErr != nil {
		return ORMWriteOutput{}, hostErr
	}
	if hostErr := runConstraintHook(ctx, r, modCtx, input.Model, "write", updated); hostErr != nil {
		return ORMWriteOutput{}, hostErr
	}

	if err := emitRecordEvent(ctx, insertClient, tx, modCtx, "orm.record.updated", input.Model, updated); err != nil {
		return ORMWriteOutput{}, &abi.HostError{Code: abi.ErrCodeUnavailable, Message: err.Error(), Retry: true}
	}

	if err := tx.Commit(); err != nil {
		return ORMWriteOutput{}, &abi.HostError{Code: abi.ErrCodeCommitFailed, Message: err.Error()}
	}

	return ORMWriteOutput{Record: updated}, nil
}

func makeORMWriteMany(r *Runtime, db *sql.DB, insertClient *river.Client[*sql.Tx]) func(ctx context.Context, m api.Module, ptr, length uint32) uint64 {
	return func(ctx context.Context, m api.Module, ptr, length uint32) uint64 {
		inst := r.InstanceForModule(m)
		modCtx := inst.ModuleContext()
		allocate := inst.allocate

		inputBytes, err := abi.ReadFromModule(m.Memory(), ptr, length)
		if err != nil {
			return abi.EncodeHostError(ctx, m, allocate, abi.MemoryFault())
		}
		var input ORMWriteManyInput
		if err := msgpack.Unmarshal(inputBytes, &input); err != nil {
			return abi.EncodeHostError(ctx, m, allocate, abi.DeserializeError(err))
		}

		out, hostErr := ORMWriteMany(ctx, r, db, insertClient, modCtx, input)
		if hostErr != nil {
			return abi.EncodeHostError(ctx, m, allocate, hostErr)
		}
		return abi.WriteToModule(ctx, m, allocate, out)
	}
}

// ORMWriteMany applies the same vals to every ID in one transaction, no
// etag check (bulk operations don't have a single "the" etag to check
// against). A missing ID or a validation failure aborts the whole
// transaction — matches the AC's "a single record's validation failure
// aborts the whole batch" and avoids silently skipping an ID a caller
// explicitly asked to update. One orm.record.updated event per affected
// record (not batched) — the doc's own explicit distinction from
// create_batch's batching.
func ORMWriteMany(ctx context.Context, r *Runtime, db *sql.DB, insertClient *river.Client[*sql.Tx], modCtx *ModuleContext, input ORMWriteManyInput) (ExecResult, *abi.HostError) {
	if !modCtx.Capabilities().Has(abi.CapDBWrite) {
		return ExecResult{}, abi.CapabilityDenied("db.write")
	}

	md, ok := resolveModel(modCtx, input.Model)
	if !ok {
		return ExecResult{}, &abi.HostError{Code: abi.ErrCodeModelNotFound, Message: "model " + input.Model + " is not declared by this module"}
	}
	if md.Backend == model.BackendTransient {
		return ExecResult{}, &abi.HostError{Code: abi.ErrCodeUnavailable, Message: "write_many is not supported for Transient-backed models"}
	}
	if hostErr := validateRequired(md, input.Record, false); hostErr != nil {
		return ExecResult{}, hostErr
	}

	pkCol, ok := primaryKeyColumn(md)
	if !ok {
		return ExecResult{}, &abi.HostError{Code: abi.ErrCodeModelNotFound, Message: "model " + input.Model + " declares no primary key field"}
	}

	record := make(map[string]any, len(input.Record)+1)
	maps.Copy(record, input.Record)
	if hasField(md, "etag") {
		record["etag"] = uuid.Must(uuid.NewV7()).String()
	}

	tx, err := beginTenantScopedWrite(ctx, db, modCtx)
	if err != nil {
		return ExecResult{}, &abi.HostError{Code: abi.ErrCodeUnavailable, Message: err.Error(), Retry: true}
	}
	defer func() { _ = tx.Rollback() }()

	result, hostErr := writeManyIDsTx(ctx, tx, r, insertClient, modCtx, md, pkCol, input.Model, input.IDs, record)
	if hostErr != nil {
		return ExecResult{}, hostErr
	}

	if err := tx.Commit(); err != nil {
		return ExecResult{}, &abi.HostError{Code: abi.ErrCodeCommitFailed, Message: err.Error()}
	}

	return result, nil
}

func makeORMWriteWhere(r *Runtime, db *sql.DB, insertClient *river.Client[*sql.Tx]) func(ctx context.Context, m api.Module, ptr, length uint32) uint64 {
	return func(ctx context.Context, m api.Module, ptr, length uint32) uint64 {
		inst := r.InstanceForModule(m)
		modCtx := inst.ModuleContext()
		allocate := inst.allocate

		inputBytes, err := abi.ReadFromModule(m.Memory(), ptr, length)
		if err != nil {
			return abi.EncodeHostError(ctx, m, allocate, abi.MemoryFault())
		}
		var input ORMWriteWhereInput
		if err := msgpack.Unmarshal(inputBytes, &input); err != nil {
			return abi.EncodeHostError(ctx, m, allocate, abi.DeserializeError(err))
		}

		out, hostErr := ORMWriteWhere(ctx, r, db, insertClient, modCtx, input)
		if hostErr != nil {
			return abi.EncodeHostError(ctx, m, allocate, hostErr)
		}
		return abi.WriteToModule(ctx, m, allocate, out)
	}
}

// ORMWriteWhere resolves matching IDs server-side (compileDomain, the
// same domain-to-SQL compiler the read pipeline already uses — never
// string-interpolating the caller's domain) and applies input.Record to
// each inside one transaction, sharing writeManyIDsTx with ORMWriteMany.
// A domain matching zero rows is a legitimate, non-error result
// (ExecResult{Count: 0}) — unlike single write-by-ID, a bulk "where" that
// happens to match nothing isn't exceptional.
func ORMWriteWhere(ctx context.Context, r *Runtime, db *sql.DB, insertClient *river.Client[*sql.Tx], modCtx *ModuleContext, input ORMWriteWhereInput) (ExecResult, *abi.HostError) {
	if !modCtx.Capabilities().Has(abi.CapDBWrite) {
		return ExecResult{}, abi.CapabilityDenied("db.write")
	}

	md, ok := resolveModel(modCtx, input.Model)
	if !ok {
		return ExecResult{}, &abi.HostError{Code: abi.ErrCodeModelNotFound, Message: "model " + input.Model + " is not declared by this module"}
	}
	if md.Backend == model.BackendTransient {
		return ExecResult{}, &abi.HostError{Code: abi.ErrCodeUnavailable, Message: "write_where is not supported for Transient-backed models"}
	}
	if hostErr := validateRequired(md, input.Record, false); hostErr != nil {
		return ExecResult{}, hostErr
	}

	pkCol, ok := primaryKeyColumn(md)
	if !ok {
		return ExecResult{}, &abi.HostError{Code: abi.ErrCodeModelNotFound, Message: "model " + input.Model + " declares no primary key field"}
	}

	whereFrag, whereArgs, hostErr := compileDomain(input.Domain)
	if hostErr != nil {
		return ExecResult{}, hostErr
	}

	record := make(map[string]any, len(input.Record)+1)
	maps.Copy(record, input.Record)
	if hasField(md, "etag") {
		record["etag"] = uuid.Must(uuid.NewV7()).String()
	}

	tx, err := beginTenantScopedWrite(ctx, db, modCtx)
	if err != nil {
		return ExecResult{}, &abi.HostError{Code: abi.ErrCodeUnavailable, Message: err.Error(), Retry: true}
	}
	defer func() { _ = tx.Rollback() }()

	table := quoteIdentORM(tableNameForORM(md))
	pkColQuoted := quoteIdentORM(pkCol)
	rows, err := tx.QueryContext(ctx, fmt.Sprintf("SELECT %s FROM %s WHERE %s", pkColQuoted, table, whereFrag), whereArgs...)
	if err != nil {
		return ExecResult{}, &abi.HostError{Code: abi.ErrCodeUnavailable, Message: err.Error()}
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return ExecResult{}, &abi.HostError{Code: abi.ErrCodeUnavailable, Message: err.Error()}
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return ExecResult{}, &abi.HostError{Code: abi.ErrCodeUnavailable, Message: err.Error()}
	}

	result, hostErr := writeManyIDsTx(ctx, tx, r, insertClient, modCtx, md, pkCol, input.Model, ids, record)
	if hostErr != nil {
		return ExecResult{}, hostErr
	}

	if err := tx.Commit(); err != nil {
		return ExecResult{}, &abi.HostError{Code: abi.ErrCodeCommitFailed, Message: err.Error()}
	}

	return result, nil
}

func makeORMUnlink(r *Runtime, db *sql.DB, insertClient *river.Client[*sql.Tx], cacheClient *cache.Client) func(ctx context.Context, m api.Module, ptr, length uint32) uint64 {
	return func(ctx context.Context, m api.Module, ptr, length uint32) uint64 {
		inst := r.InstanceForModule(m)
		modCtx := inst.ModuleContext()
		allocate := inst.allocate

		inputBytes, err := abi.ReadFromModule(m.Memory(), ptr, length)
		if err != nil {
			return abi.EncodeHostError(ctx, m, allocate, abi.MemoryFault())
		}
		var input ORMUnlinkInput
		if err := msgpack.Unmarshal(inputBytes, &input); err != nil {
			return abi.EncodeHostError(ctx, m, allocate, abi.DeserializeError(err))
		}

		out, hostErr := ORMUnlink(ctx, r, db, insertClient, cacheClient, modCtx, input)
		if hostErr != nil {
			return abi.EncodeHostError(ctx, m, allocate, hostErr)
		}
		return abi.WriteToModule(ctx, m, allocate, out)
	}
}

// ORMUnlink is host.orm unlink's plain-Go core — see ORMSearch's doc
// comment (host_orm.go) for the shared-entry-point rationale. Branches to
// transientUnlink (host_orm_transient.go) for Transient-backed models
// internally. Fetches the row before deleting it (fetchRowByPK,
// goerp#377) so the OnDelete constraint hook (goerp#378) can run against
// it — and, per go-sdk-reference.md §22, before either the FK check or
// the delete SQL itself, unlike OnCreate/OnWrite's after-the-write
// placement (see this file's own package doc comment for why those two
// differ).
func ORMUnlink(ctx context.Context, r *Runtime, db *sql.DB, insertClient *river.Client[*sql.Tx], cacheClient *cache.Client, modCtx *ModuleContext, input ORMUnlinkInput) (ORMUnlinkOutput, *abi.HostError) {
	if !modCtx.Capabilities().Has(abi.CapDBWrite) {
		return ORMUnlinkOutput{}, abi.CapabilityDenied("db.write")
	}

	md, ok := resolveModel(modCtx, input.Model)
	if !ok {
		return ORMUnlinkOutput{}, &abi.HostError{Code: abi.ErrCodeModelNotFound, Message: "model " + input.Model + " is not declared by this module"}
	}

	if md.Backend == model.BackendTransient {
		return transientUnlink(ctx, cacheClient, modCtx, input.Model, input.ID)
	}

	pkCol, ok := primaryKeyColumn(md)
	if !ok {
		return ORMUnlinkOutput{}, &abi.HostError{Code: abi.ErrCodeModelNotFound, Message: "model " + input.Model + " declares no primary key field"}
	}

	tx, err := beginTenantScopedWrite(ctx, db, modCtx)
	if err != nil {
		return ORMUnlinkOutput{}, &abi.HostError{Code: abi.ErrCodeUnavailable, Message: err.Error(), Retry: true}
	}
	defer func() { _ = tx.Rollback() }()

	table := quoteIdentORM(tableNameForORM(md))
	pkColQuoted := quoteIdentORM(pkCol)

	existing, hostErr := fetchRowByPK(ctx, tx, md, pkCol, input.ID)
	if hostErr != nil {
		return ORMUnlinkOutput{}, hostErr
	}
	if hostErr := runConstraintHook(ctx, r, modCtx, input.Model, "delete", existing); hostErr != nil {
		return ORMUnlinkOutput{}, hostErr
	}

	var deletedID string
	if hasField(md, "deleted_at") {
		deleteSQL := fmt.Sprintf("UPDATE %s SET %s = NOW() WHERE %s = $1 AND %s IS NULL RETURNING %s",
			table, quoteIdentORM("deleted_at"), pkColQuoted, quoteIdentORM("deleted_at"), pkColQuoted)
		err = tx.QueryRowContext(ctx, deleteSQL, input.ID).Scan(&deletedID)
	} else {
		deleteSQL := fmt.Sprintf("DELETE FROM %s WHERE %s = $1 RETURNING %s", table, pkColQuoted, pkColQuoted)
		err = tx.QueryRowContext(ctx, deleteSQL, input.ID).Scan(&deletedID)
	}
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ORMUnlinkOutput{}, &abi.HostError{Code: abi.ErrCodeNotFound, Message: "record not found"}
		}
		return ORMUnlinkOutput{}, translateWriteError(err, md)
	}

	if err := emitRecordEvent(ctx, insertClient, tx, modCtx, "orm.record.deleted", input.Model, map[string]any{"id": deletedID}); err != nil {
		return ORMUnlinkOutput{}, &abi.HostError{Code: abi.ErrCodeUnavailable, Message: err.Error(), Retry: true}
	}

	if err := tx.Commit(); err != nil {
		return ORMUnlinkOutput{}, &abi.HostError{Code: abi.ErrCodeCommitFailed, Message: err.Error()}
	}

	return ORMUnlinkOutput{Deleted: true}, nil
}

// beginTenantScopedWrite is beginTenantScopedRead without ReadOnly — same
// session-var scoping so RLS applies automatically to writes too.
func beginTenantScopedWrite(ctx context.Context, db *sql.DB, modCtx *ModuleContext) (*sql.Tx, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `SELECT set_config('search_path', $1, true),
		set_config('app.current_user_id', $2, true),
		set_config('app.current_user_contact_id', $3, true),
		set_config('app.current_user_roles', $4, true)`,
		"tenant_"+modCtx.TenantSlug+", public",
		modCtx.UserID, modCtx.ContactID, strings.Join(modCtx.Roles, ","),
	); err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	return tx, nil
}

// validateRequired checks every .Required() field. requireAll (create)
// demands presence; write (requireAll=false) only rejects a required
// field explicitly present in the diff with a null value — a field
// simply absent from the diff is an unchanged, already-valid column.
func validateRequired(md model.ModelDeclaration, record map[string]any, requireAll bool) *abi.HostError {
	for _, f := range md.Fields {
		if !f.Def.IsRequired {
			continue
		}
		if f.Def.DefaultExpr != nil {
			// A DB-side default satisfies NOT NULL on its own when the
			// caller omits the field — only an explicit null in the diff
			// is a genuine violation, handled by the present-but-nil
			// check below.
			if _, present := record[f.Name]; !present {
				continue
			}
		}
		v, present := record[f.Name]
		if !present {
			if requireAll {
				return &abi.HostError{Code: abi.ErrCodeValidationFailed, Message: "field " + f.Name + " is required", Details: map[string]any{"field": f.Name}}
			}
			continue
		}
		if v == nil {
			return &abi.HostError{Code: abi.ErrCodeValidationFailed, Message: "field " + f.Name + " is required", Details: map[string]any{"field": f.Name}}
		}
	}
	return nil
}

// hasField reports whether md declares a field literally named name —
// used to detect WithStandardFields() (a "deleted_at" field means
// soft-delete; an "etag" field means optimistic locking applies).
func hasField(md model.ModelDeclaration, name string) bool {
	for _, f := range md.Fields {
		if f.Name == name {
			return true
		}
	}
	return false
}

// acquireSequenceFields resolves a period key and acquires a fresh
// counter value (goerp#340) for every Sequence-kind field on md that
// isn't already present in record — mutating record in place. Runs on
// tx, immediately before the INSERT, so the lock is held for the
// shortest time possible.
func acquireSequenceFields(ctx context.Context, tx *sql.Tx, tenantSlug, modelName string, md model.ModelDeclaration, record map[string]any) *abi.HostError {
	for _, f := range md.Fields {
		if f.Def.Kind != model.KindSequence {
			continue
		}
		if _, present := record[f.Name]; present {
			continue
		}
		periodKey := orm.ResolvePeriodKey(f.Def.SequenceFormat, time.Now())
		next, err := orm.AcquireNext(ctx, tx, tenantSlug, modelName, f.Name, periodKey)
		if err != nil {
			return &abi.HostError{Code: abi.ErrCodeUnavailable, Message: err.Error(), Retry: true}
		}
		record[f.Name] = next
	}
	return nil
}

// buildAssignment validates record's keys against md's declared fields
// and returns parallel (quoted column name, positional placeholder
// value) slices ready to interpolate into an INSERT's column/VALUES
// lists or an UPDATE's SET list — the caller decides which shape to
// build from the returned columns/args. A .Computed() field — Store(true)
// or Store(false) alike — is never directly settable
// (go-sdk-reference.md §22 "Computed field recomputation"): orm.write's
// own recomputeAfterWrite is the only path that ever assigns one.
func buildAssignment(md model.ModelDeclaration, record map[string]any) (cols []string, args []any, hostErr *abi.HostError) {
	fields := make(map[string]model.FieldDef, len(md.Fields))
	for _, f := range md.Fields {
		fields[f.Name] = f.Def
	}
	for k, v := range record {
		def, known := fields[k]
		if !known {
			return nil, nil, &abi.HostError{Code: abi.ErrCodeValidationFailed, Message: "unknown field " + k, Details: map[string]any{"field": k}}
		}
		if def.IsComputed {
			return nil, nil, &abi.HostError{Code: abi.ErrCodeFieldNotWritable, Message: "field " + k + " is computed and cannot be written directly", Details: map[string]any{"field": k}}
		}
		cols = append(cols, quoteIdentORM(k))
		args = append(args, v)
	}
	return cols, args, nil
}

// createOneRecordTx inserts one row on tx — the shared core of
// ORMCreate/ORMCreateBatch/ORMFirstOrCreate. Caller is responsible for
// validateRequired/acquireSequenceFields beforehand and event emission
// afterward (the three callers emit differently: single, per-record, or
// batched). When onConflict is set, its target is validated against a
// real declared unique index (primaryKeyColumn or an IsUnique md.Indexes
// entry) before building the INSERT — an unmatched target is
// orm.conflict_target_invalid, never a silent full-table-scan fallback.
//
// RETURNING includes "(xmax = 0) AS __inserted" — the standard Postgres
// idiom for "this row was inserted by this command, not updated via the
// ON CONFLICT DO UPDATE arm" (xmax is 0 only for a row this exact command
// just inserted). inserted tells the caller which event type to emit for
// this row. An OnConflictIgnore hit returns (nil, false, nil) — a
// skipped conflict is not an error, just nothing to report.
func createOneRecordTx(ctx context.Context, tx *sql.Tx, md model.ModelDeclaration, qualifiedModel string, record map[string]any, onConflict *OnConflictOption) (map[string]any, bool, *abi.HostError) {
	cols, args, hostErr := buildAssignment(md, record)
	if hostErr != nil {
		return nil, false, hostErr
	}
	if len(cols) == 0 {
		return nil, false, &abi.HostError{Code: abi.ErrCodeValidationFailed, Message: "record has no fields to insert"}
	}
	placeholders := make([]string, len(args))
	for i := range args {
		placeholders[i] = fmt.Sprintf("$%d", i+1)
	}

	table := quoteIdentORM(tableNameForORM(md))
	insertSQL := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)",
		table, strings.Join(cols, ", "), strings.Join(placeholders, ", "))

	if onConflict != nil {
		targetCols, hostErr := validateOnConflictTarget(md, qualifiedModel, onConflict.Fields)
		if hostErr != nil {
			return nil, false, hostErr
		}
		quotedTarget := make([]string, len(targetCols))
		for i, c := range targetCols {
			quotedTarget[i] = quoteIdentORM(c)
		}
		switch onConflict.Policy {
		case "ignore":
			insertSQL += fmt.Sprintf(" ON CONFLICT (%s) DO NOTHING", strings.Join(quotedTarget, ", "))
		case "update":
			setClauses := make([]string, len(cols))
			for i, c := range cols {
				setClauses[i] = fmt.Sprintf("%s = EXCLUDED.%s", c, c)
			}
			insertSQL += fmt.Sprintf(" ON CONFLICT (%s) DO UPDATE SET %s", strings.Join(quotedTarget, ", "), strings.Join(setClauses, ", "))
		default:
			return nil, false, &abi.HostError{Code: abi.ErrCodeValidationFailed, Message: "on_conflict.policy must be \"ignore\" or \"update\", got " + onConflict.Policy}
		}
	}

	insertSQL += " RETURNING *, (xmax = 0) AS __inserted"

	rows, err := tx.QueryContext(ctx, insertSQL, args...)
	if err != nil {
		return nil, false, translateWriteError(err, md)
	}
	created, err := scanRowsToMaps(rows)
	if err != nil {
		return nil, false, &abi.HostError{Code: abi.ErrCodeUnavailable, Message: err.Error()}
	}
	if len(created) == 0 {
		return nil, false, nil
	}
	if len(created) != 1 {
		return nil, false, &abi.HostError{Code: abi.ErrCodeUnavailable, Message: "insert did not return exactly one row"}
	}

	row := created[0]
	inserted, _ := row["__inserted"].(bool)
	delete(row, "__inserted")

	return row, inserted, nil
}

// validateOnConflictTarget resolves fields against a real declared unique
// index — the model's single-column primary key, or a md.Indexes entry
// with Def.IsUnique whose column set matches exactly (order-independent).
// No match is orm.conflict_target_invalid rather than a silent
// full-table-scan fallback for conflict detection.
func validateOnConflictTarget(md model.ModelDeclaration, qualifiedModel string, fields []string) ([]string, *abi.HostError) {
	if len(fields) == 0 {
		return nil, &abi.HostError{Code: abi.ErrCodeConflictTargetInvalid, Message: "on_conflict.fields must not be empty"}
	}
	sorted := slices.Clone(fields)
	slices.Sort(sorted)

	if pkCol, ok := primaryKeyColumn(md); ok && len(sorted) == 1 && sorted[0] == pkCol {
		return fields, nil
	}
	for _, idx := range md.Indexes {
		if !idx.Def.IsUnique {
			continue
		}
		cols := slices.Clone(idx.Def.Columns)
		slices.Sort(cols)
		if slices.Equal(cols, sorted) {
			return fields, nil
		}
	}
	return nil, &abi.HostError{Code: abi.ErrCodeConflictTargetInvalid, Message: "on_conflict.fields " + strings.Join(fields, ", ") + " does not match any declared unique index on " + qualifiedModel}
}

// writeOneRecordTx updates the row identified by id on tx — the shared
// core of ORMWrite/ORMWriteMany/ORMWriteWhere. expectedEtag == "" skips
// the etag-scoped WHERE clause entirely (the bulk callers' "no etag
// check" semantics); a non-empty expectedEtag adds it, matching single
// write's optimistic-lock behavior.
func writeOneRecordTx(ctx context.Context, tx *sql.Tx, md model.ModelDeclaration, pkCol, id string, record map[string]any, expectedEtag string) (map[string]any, *abi.HostError) {
	sets, args, hostErr := buildAssignment(md, record)
	if hostErr != nil {
		return nil, hostErr
	}
	if len(sets) == 0 {
		return nil, &abi.HostError{Code: abi.ErrCodeValidationFailed, Message: "record has no fields to update"}
	}

	table := quoteIdentORM(tableNameForORM(md))
	pkColQuoted := quoteIdentORM(pkCol)
	setClauses := make([]string, len(sets))
	for i, col := range sets {
		setClauses[i] = col + " = $" + fmt.Sprint(i+1)
	}

	args = append(args, id)
	whereClause := fmt.Sprintf("%s = $%d", pkColQuoted, len(args))
	if expectedEtag != "" && hasField(md, "etag") {
		args = append(args, expectedEtag)
		whereClause += fmt.Sprintf(" AND %s = $%d", quoteIdentORM("etag"), len(args))
	}

	updateSQL := fmt.Sprintf("UPDATE %s SET %s WHERE %s RETURNING *",
		table, strings.Join(setClauses, ", "), whereClause)

	rows, err := tx.QueryContext(ctx, updateSQL, args...)
	if err != nil {
		return nil, translateWriteError(err, md)
	}
	updated, err := scanRowsToMaps(rows)
	if err != nil {
		return nil, &abi.HostError{Code: abi.ErrCodeUnavailable, Message: err.Error()}
	}

	if len(updated) == 0 {
		return nil, diagnoseZeroRowWrite(ctx, tx, table, pkColQuoted, id, expectedEtag)
	}
	return updated[0], nil
}

// writeManyIDsTx applies record to every id on tx, emitting one
// orm.record.updated event per affected record (not batched) — shared by
// ORMWriteMany (caller-supplied IDs) and ORMWriteWhere (IDs resolved from
// a domain first). A missing ID or validation failure aborts the whole
// transaction, matching the AC's all-or-nothing requirement.
func writeManyIDsTx(ctx context.Context, tx *sql.Tx, r *Runtime, insertClient *river.Client[*sql.Tx], modCtx *ModuleContext, md model.ModelDeclaration, pkCol, qualifiedModel string, ids []string, record map[string]any) (ExecResult, *abi.HostError) {
	changedFields := changedFieldNames(record)
	affected := make([]string, 0, len(ids))
	for _, id := range ids {
		updated, hostErr := writeOneRecordTx(ctx, tx, md, pkCol, id, record, "")
		if hostErr != nil {
			return ExecResult{}, hostErr
		}
		if hostErr := recomputeAfterWrite(ctx, tx, r, modCtx, qualifiedModel, md, changedFields, updated); hostErr != nil {
			return ExecResult{}, hostErr
		}
		if hostErr := runConstraintHook(ctx, r, modCtx, qualifiedModel, "write", updated); hostErr != nil {
			return ExecResult{}, hostErr
		}
		if err := emitRecordEvent(ctx, insertClient, tx, modCtx, "orm.record.updated", qualifiedModel, updated); err != nil {
			return ExecResult{}, &abi.HostError{Code: abi.ErrCodeUnavailable, Message: err.Error(), Retry: true}
		}
		affected = append(affected, id)
	}
	return ExecResult{Count: len(affected), IDs: affected}, nil
}

// changedFieldNames returns record's keys as a slice — for create/
// create_batch/first_or_create, record is the row actually inserted (every
// field is "new," so every dependency is trivially satisfied); for
// write/write_many/write_where, record is the literal diff the caller
// supplied, matching host-abi-reference.md's own "changed_fields ...
// computes this from the vals map" framing for event emission.
func changedFieldNames(record map[string]any) []string {
	names := make([]string, 0, len(record))
	for k := range record {
		names = append(names, k)
	}
	return names
}

// recomputeAfterWrite runs every Store(true) computed field that depends
// on a field in changedFields — same-record and one-hop Many2One
// dependencies alike — inside tx, before it commits
// (go-sdk-reference.md §22 "Computed field recomputation"). No
// recursion: a recomputed field's own new value is applied directly and
// never re-triggers a second recompute pass, matching the AC's
// single-hop-only scope.
func recomputeAfterWrite(ctx context.Context, tx *sql.Tx, r *Runtime, modCtx *ModuleContext, qualifiedModel string, md model.ModelDeclaration, changedFields []string, row map[string]any) *abi.HostError {
	idx := modCtx.ComputedIndex()
	if idx == nil || len(changedFields) == 0 {
		return nil
	}

	for _, dep := range idx.Lookup(qualifiedModel, changedFields) {
		depPK, ok := primaryKeyColumn(dep.ModelDecl)
		if !ok {
			continue
		}

		if dep.ViaFKField == "" {
			// Same-record: the dependent field lives on the row just
			// written.
			value, hostErr := invokeCompute(ctx, r, modCtx, dep, row)
			if hostErr != nil {
				return hostErr
			}
			if hostErr := applyComputedValue(ctx, tx, dep.ModelDecl, depPK, row[depPK], dep.Field, value); hostErr != nil {
				return hostErr
			}
			row[dep.Field] = value
			continue
		}

		// Many2One hop: dep.ModelDecl's own ViaFKField column points at
		// the model just written. Find every dependent row referencing
		// it and recompute each independently.
		writtenPK, ok := primaryKeyColumn(md)
		if !ok {
			continue
		}
		depIDs, err := fkReferencingIDs(ctx, tx, dep.ModelDecl, depPK, dep.ViaFKField, row[writtenPK])
		if err != nil {
			return &abi.HostError{Code: abi.ErrCodeUnavailable, Message: err.Error()}
		}

		for _, depID := range depIDs {
			depRow, hostErr := fetchRowByPK(ctx, tx, dep.ModelDecl, depPK, depID)
			if hostErr != nil {
				return hostErr
			}
			value, hostErr := invokeCompute(ctx, r, modCtx, dep, depRow)
			if hostErr != nil {
				return hostErr
			}
			if hostErr := applyComputedValue(ctx, tx, dep.ModelDecl, depPK, depID, dep.Field, value); hostErr != nil {
				return hostErr
			}
		}
	}
	return nil
}

// fkReferencingIDs returns every depMD row's primary key where its fkCol
// equals fkValue — the Many2One-hop case's "which dependent records point
// at the record that just changed" query.
func fkReferencingIDs(ctx context.Context, tx *sql.Tx, depMD model.ModelDeclaration, depPK, fkCol string, fkValue any) ([]any, error) {
	table := quoteIdentORM(tableNameForORM(depMD))
	sqlStr := fmt.Sprintf("SELECT %s FROM %s WHERE %s = $1", quoteIdentORM(depPK), table, quoteIdentORM(fkCol))
	rows, err := tx.QueryContext(ctx, sqlStr, fkValue)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []any
	for rows.Next() {
		var id any
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// fetchRowByPK re-selects one full row by primary key on tx, so a
// Many2One-hop compute function sees the dependent record's current
// column values rather than a stale copy.
func fetchRowByPK(ctx context.Context, tx *sql.Tx, md model.ModelDeclaration, pkCol string, pkValue any) (map[string]any, *abi.HostError) {
	table := quoteIdentORM(tableNameForORM(md))
	sqlStr := fmt.Sprintf("SELECT * FROM %s WHERE %s = $1", table, quoteIdentORM(pkCol))
	rows, err := tx.QueryContext(ctx, sqlStr, pkValue)
	if err != nil {
		return nil, &abi.HostError{Code: abi.ErrCodeUnavailable, Message: err.Error()}
	}
	records, err := scanRowsToMaps(rows)
	if err != nil {
		return nil, &abi.HostError{Code: abi.ErrCodeUnavailable, Message: err.Error()}
	}
	if len(records) == 0 {
		return nil, &abi.HostError{Code: abi.ErrCodeNotFound, Message: "record not found"}
	}
	return records[0], nil
}

// applyComputedValue writes a single computed field's new value directly
// — never routed back through writeOneRecordTx, which would rotate the
// row's etag, re-emit orm.record.updated, and re-trigger recompute
// (single-hop only, no cascading, per the AC).
func applyComputedValue(ctx context.Context, tx *sql.Tx, md model.ModelDeclaration, pkCol string, pkValue any, field string, value any) *abi.HostError {
	table := quoteIdentORM(tableNameForORM(md))
	sqlStr := fmt.Sprintf("UPDATE %s SET %s = $1 WHERE %s = $2", table, quoteIdentORM(field), quoteIdentORM(pkCol))
	if _, err := tx.ExecContext(ctx, sqlStr, value, pkValue); err != nil {
		return translateWriteError(err, md)
	}
	return nil
}

// translateWriteError maps a Postgres write failure to the structured
// orm.* error the caller should see, falling back to a plain
// orm.unavailable for anything it doesn't recognize.
func translateWriteError(err error, md model.ModelDeclaration) *abi.HostError {
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok {
		switch pgErr.Code {
		case "23505": // unique_violation
			for _, idx := range md.Indexes {
				if idx.Def.IsUnique && idx.Name == pgErr.ConstraintName {
					return &abi.HostError{
						Code:    abi.ErrCodeUniqueViolation,
						Message: "unique constraint violated on " + strings.Join(idx.Def.Columns, ", "),
						Details: map[string]any{"index": idx.Name, "fields": idx.Def.Columns},
					}
				}
			}
			return &abi.HostError{Code: abi.ErrCodeUniqueViolation, Message: pgErr.Message}
		case "23503", "23001": // foreign_key_violation, restrict_violation
			return &abi.HostError{Code: abi.ErrCodeForeignKeyViolation, Message: pgErr.Message, Details: map[string]any{"constraint": pgErr.ConstraintName}}
		}
	}
	return &abi.HostError{Code: abi.ErrCodeUnavailable, Message: err.Error()}
}

// diagnoseZeroRowWrite disambiguates a 0-row etag-scoped UPDATE:
// re-selecting by ID alone (still inside tx, so this sees the same
// RLS-filtered view) tells apart a stale etag (row exists,
// orm.etag_mismatch) from a genuinely missing or RLS-filtered row
// (orm.not_found) — the same 404-shaped-denial rule this codebase's read
// paths already follow (security-model.md).
func diagnoseZeroRowWrite(ctx context.Context, tx *sql.Tx, table, pkColQuoted, id, expectedEtag string) *abi.HostError {
	if expectedEtag == "" {
		return &abi.HostError{Code: abi.ErrCodeNotFound, Message: "record not found"}
	}
	var exists bool
	checkSQL := fmt.Sprintf("SELECT EXISTS (SELECT 1 FROM %s WHERE %s = $1)", table, pkColQuoted)
	if err := tx.QueryRowContext(ctx, checkSQL, id).Scan(&exists); err != nil {
		return &abi.HostError{Code: abi.ErrCodeUnavailable, Message: err.Error()}
	}
	if exists {
		return &abi.HostError{Code: abi.ErrCodeEtagMismatch, Message: "record has been modified since it was last read"}
	}
	return &abi.HostError{Code: abi.ErrCodeNotFound, Message: "record not found"}
}

// emitRecordEvent inserts an orm.record.* EventDelivery job on tx,
// bypassing host.event.emit_tx's module-declared-emits gate entirely —
// these are engine-emitted lifecycle events, not module-authored ones,
// the same reasoning event-system.md gives system.* events for not
// needing a manifest declaration. No idempotency-key dedup either: every
// write is already its own distinct transaction.
func emitRecordEvent(ctx context.Context, insertClient *river.Client[*sql.Tx], tx *sql.Tx, modCtx *ModuleContext, eventName, modelName string, record map[string]any) error {
	payload, err := msgpack.Marshal(map[string]any{"model": modelName, "record": record})
	if err != nil {
		return err
	}
	eventID := uuid.Must(uuid.NewV7())
	return insertEventDeliveryTx(ctx, insertClient, tx, eventID, eventName, 1,
		modCtx.ModuleName, modCtx.TenantID, modCtx.UserID, modCtx.TraceID, payload, 0, nil)
}

// emitBatchRecordEvent is emitRecordEvent's plural form for
// ORMCreateBatch — one event listing every record in records, matching
// the doc's "batched into one event with all IDs for create_batch, not
// one event per row".
func emitBatchRecordEvent(ctx context.Context, insertClient *river.Client[*sql.Tx], tx *sql.Tx, modCtx *ModuleContext, eventName, modelName string, records []map[string]any) error {
	payload, err := msgpack.Marshal(map[string]any{"model": modelName, "records": records})
	if err != nil {
		return err
	}
	eventID := uuid.Must(uuid.NewV7())
	return insertEventDeliveryTx(ctx, insertClient, tx, eventID, eventName, 1,
		modCtx.ModuleName, modCtx.TenantID, modCtx.UserID, modCtx.TraceID, payload, 0, nil)
}
