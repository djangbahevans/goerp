package wasm

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/djangbahevans/goerp/internal/engine/cache"
	"github.com/djangbahevans/goerp/internal/engine/computed"
	"github.com/djangbahevans/goerp/internal/engine/fieldsec"
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
// transaction as the triggering create/write, for same-record, one-hop
// Many2One, and one-hop One2Many dependencies alike — goerp#388's
// One2Many case additionally recomputes from ORMUnlink via
// recomputeParentsAfterChildUnlink, since a child's own deletion is a
// valid recompute trigger the other two hop kinds never need), and
// orm.RegisterConstraint hooks (goerp#378, runConstraintHook —
// host_orm_constraint.go), which run after recomputeAfterWrite so a hook
// sees the fully-recomputed row, and before event emission so a
// rejection aborts the whole transaction. Also writes one audit_log row
// per create/write/unlink on a module's own audited_tables[] (goerp#363,
// writeAuditLogEntry) — after recomputeAfterWrite/runConstraintHook so
// the row reflects the fully committed values, before event emission,
// in the same transaction.
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
	TxID       string            `msgpack:"tx_id"`
}

type ORMCreateOutput struct {
	Record map[string]any `msgpack:"record"`
}

type ORMCreateBatchInput struct {
	Model      string            `msgpack:"model"`
	Records    []map[string]any  `msgpack:"records"`
	OnConflict *OnConflictOption `msgpack:"on_conflict,omitempty"`
	TxID       string            `msgpack:"tx_id"`
}

type ORMCreateBatchOutput struct {
	Records []map[string]any `msgpack:"records"`
}

type ORMFirstOrCreateInput struct {
	Model      string         `msgpack:"model"`
	UniqueVals map[string]any `msgpack:"unique_vals"`
	CreateVals map[string]any `msgpack:"create_vals"`
	TxID       string         `msgpack:"tx_id"`
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
	TxID         string         `msgpack:"tx_id"`
}

type ORMWriteOutput struct {
	Record map[string]any `msgpack:"record"`
}

type ORMWriteManyInput struct {
	Model  string         `msgpack:"model"`
	IDs    []string       `msgpack:"ids"`
	Record map[string]any `msgpack:"record"`
	TxID   string         `msgpack:"tx_id"`
}

type ORMWriteWhereInput struct {
	Model  string         `msgpack:"model"`
	Domain string         `msgpack:"domain"`
	Record map[string]any `msgpack:"record"`
	TxID   string         `msgpack:"tx_id"`
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
	Model string   `msgpack:"model"`
	IDs   []string `msgpack:"ids"`
	TxID  string   `msgpack:"tx_id"`
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
	if hostErr := validateDynamicLinkPairs(md, record); hostErr != nil {
		return ORMCreateOutput{}, hostErr
	}

	if md.Backend == model.BackendTransient {
		return transientCreate(ctx, cacheClient, modCtx, md, input.Model, record)
	}

	tx, commit, rollback, hostErr := resolveORMWriteTx(ctx, db, modCtx, input.TxID)
	if hostErr != nil {
		return ORMCreateOutput{}, hostErr
	}
	defer rollback()

	if hostErr := checkDynamicLinkTargets(ctx, tx, modCtx, md, record); hostErr != nil {
		return ORMCreateOutput{}, hostErr
	}
	if hostErr := injectTreePathOnCreate(ctx, tx, md, record); hostErr != nil {
		return ORMCreateOutput{}, hostErr
	}
	if hostErr := acquireSequenceFields(ctx, tx, modCtx.TenantSlug, input.Model, md, record); hostErr != nil {
		return ORMCreateOutput{}, hostErr
	}

	row, inserted, hostErr := createOneRecordTx(ctx, tx, modCtx, md, input.Model, record, input.OnConflict)
	if hostErr != nil {
		return ORMCreateOutput{}, hostErr
	}
	if row == nil {
		// OnConflictIgnore skipped this row — nothing was created, so
		// there's nothing to return and no event to emit. Matches the
		// doc's own framing: a successful create call looks identical
		// whether it inserted or hit the conflict target.
		if err := commit(); err != nil {
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

	operation := "INSERT"
	if !inserted {
		// OnConflict update path: old_data isn't captured here (would
		// need a pre-conflict fetch inside createOneRecordTx) — audited
		// as an UPDATE with old_data left NULL.
		operation = "UPDATE"
	}
	if hostErr := writeAuditLogEntry(ctx, tx, modCtx, input.Model, md, operation, nil, row); hostErr != nil {
		return ORMCreateOutput{}, hostErr
	}

	eventName := "orm.record.created"
	if !inserted {
		eventName = "orm.record.updated"
	}
	if err := emitRecordEvent(ctx, insertClient, tx, modCtx, eventName, input.Model, row); err != nil {
		return ORMCreateOutput{}, &abi.HostError{Code: abi.ErrCodeUnavailable, Message: err.Error(), Retry: true}
	}

	if err := commit(); err != nil {
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

	tx, commit, rollback, hostErr := resolveORMWriteTx(ctx, db, modCtx, input.TxID)
	if hostErr != nil {
		return ORMCreateBatchOutput{}, hostErr
	}
	defer rollback()

	var all, createdForEvent, updatedForEvent []map[string]any
	for _, rec := range input.Records {
		record := make(map[string]any, len(rec))
		maps.Copy(record, rec)

		if hostErr := validateRequired(md, record, true); hostErr != nil {
			return ORMCreateBatchOutput{}, hostErr
		}
		if hostErr := validateDynamicLinkPairs(md, record); hostErr != nil {
			return ORMCreateBatchOutput{}, hostErr
		}
		if hostErr := checkDynamicLinkTargets(ctx, tx, modCtx, md, record); hostErr != nil {
			return ORMCreateBatchOutput{}, hostErr
		}
		if hostErr := injectTreePathOnCreate(ctx, tx, md, record); hostErr != nil {
			return ORMCreateBatchOutput{}, hostErr
		}
		if hostErr := acquireSequenceFields(ctx, tx, modCtx.TenantSlug, input.Model, md, record); hostErr != nil {
			return ORMCreateBatchOutput{}, hostErr
		}

		row, inserted, hostErr := createOneRecordTx(ctx, tx, modCtx, md, input.Model, record, input.OnConflict)
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
		operation := "INSERT"
		if !inserted {
			// See ORMCreate's own comment: OnConflict update path has no
			// captured old_data.
			operation = "UPDATE"
		}
		if hostErr := writeAuditLogEntry(ctx, tx, modCtx, input.Model, md, operation, nil, row); hostErr != nil {
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

	if err := commit(); err != nil {
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

// ORMFirstOrCreate matches an existing record by input.UniqueVals —
// validated against md's own declared unique indexes the same way
// validateOnConflictTarget validates OnConflictIgnore/OnConflictUpdate's
// target — and creates input.CreateVals merged with input.UniqueVals
// (UniqueVals wins on any overlapping key, so the inserted row always
// satisfies the same match a later call will look for) only on a miss,
// inside one transaction. Race-safety for concurrent callers racing the
// identical match uses a transaction-scoped Postgres advisory lock keyed
// by hash(tenant, model, msgpack-encoded sorted unique-vals pairs) — the
// general-condition counterpart to AcquireNext's (internal/engine/orm)
// keyed INSERT...ON CONFLICT DO UPDATE upsert pattern. msgpack encoding
// (rather than a naive "field=value" string join) keeps distinct
// unique-vals combinations from ever formatting to the same key, since
// separators can appear inside a field's own value. The lock only
// serializes callers racing the identical (tenant, model, unique-vals)
// triple; it's released automatically at commit/rollback.
//
// Kept as this SELECT-then-INSERT shape rather than createOneRecordTx's
// INSERT...ON CONFLICT DO NOTHING (used by OnConflictIgnore) deliberately:
// go-sdk-reference.md §6a documents CreateVals as "only used when
// created," meaning a hit must never run validateRequired/DynamicLink/
// sequence-acquisition against a CreateVals map that's allowed to be
// incomplete when the record already exists — an INSERT-first attempt
// can't know that until after already running them.
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

	fields := slices.Sorted(maps.Keys(input.UniqueVals))
	if _, hostErr := validateOnConflictTarget(md, input.Model, fields, "unique_vals"); hostErr != nil {
		return ORMFirstOrCreateOutput{}, hostErr
	}

	whereConds := make([]string, len(fields))
	whereArgs := make([]any, len(fields))
	lockKeyPairs := make([][2]any, len(fields))
	for i, f := range fields {
		v := input.UniqueVals[f]
		if v == nil {
			return ORMFirstOrCreateOutput{}, &abi.HostError{Code: abi.ErrCodeValidationFailed, Message: "unique_vals[" + f + "] must not be nil"}
		}
		whereConds[i] = fmt.Sprintf("%s = $%d", quoteIdentORM(f), i+1)
		whereArgs[i] = v
		lockKeyPairs[i] = [2]any{f, v}
	}
	whereFrag := strings.Join(whereConds, " AND ")

	lockKeyBytes, err := msgpack.Marshal(lockKeyPairs)
	if err != nil {
		return ORMFirstOrCreateOutput{}, &abi.HostError{Code: abi.ErrCodeUnavailable, Message: err.Error()}
	}

	tx, commit, rollback, hostErr := resolveORMWriteTx(ctx, db, modCtx, input.TxID)
	if hostErr != nil {
		return ORMFirstOrCreateOutput{}, hostErr
	}
	defer rollback()

	lockKey := modCtx.TenantSlug + ":" + input.Model + ":" + hex.EncodeToString(lockKeyBytes)
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
		if err := commit(); err != nil {
			return ORMFirstOrCreateOutput{}, &abi.HostError{Code: abi.ErrCodeCommitFailed, Message: err.Error()}
		}
		return ORMFirstOrCreateOutput{Record: found[0], Created: false}, nil
	}

	record := make(map[string]any, len(input.UniqueVals)+len(input.CreateVals))
	maps.Copy(record, input.CreateVals)
	maps.Copy(record, input.UniqueVals)
	if hostErr := validateRequired(md, record, true); hostErr != nil {
		return ORMFirstOrCreateOutput{}, hostErr
	}
	if hostErr := validateDynamicLinkPairs(md, record); hostErr != nil {
		return ORMFirstOrCreateOutput{}, hostErr
	}
	if hostErr := checkDynamicLinkTargets(ctx, tx, modCtx, md, record); hostErr != nil {
		return ORMFirstOrCreateOutput{}, hostErr
	}
	if hostErr := injectTreePathOnCreate(ctx, tx, md, record); hostErr != nil {
		return ORMFirstOrCreateOutput{}, hostErr
	}
	if hostErr := acquireSequenceFields(ctx, tx, modCtx.TenantSlug, input.Model, md, record); hostErr != nil {
		return ORMFirstOrCreateOutput{}, hostErr
	}

	row, _, hostErr := createOneRecordTx(ctx, tx, modCtx, md, input.Model, record, nil)
	if hostErr != nil {
		return ORMFirstOrCreateOutput{}, hostErr
	}

	if hostErr := recomputeAfterWrite(ctx, tx, r, modCtx, input.Model, md, changedFieldNames(row), row); hostErr != nil {
		return ORMFirstOrCreateOutput{}, hostErr
	}
	if hostErr := runConstraintHook(ctx, r, modCtx, input.Model, "create", row); hostErr != nil {
		return ORMFirstOrCreateOutput{}, hostErr
	}
	if hostErr := writeAuditLogEntry(ctx, tx, modCtx, input.Model, md, "INSERT", nil, row); hostErr != nil {
		return ORMFirstOrCreateOutput{}, hostErr
	}

	if err := emitRecordEvent(ctx, insertClient, tx, modCtx, "orm.record.created", input.Model, row); err != nil {
		return ORMFirstOrCreateOutput{}, &abi.HostError{Code: abi.ErrCodeUnavailable, Message: err.Error(), Retry: true}
	}
	if err := commit(); err != nil {
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
	if hostErr := validateDynamicLinkPairs(md, input.Record); hostErr != nil {
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

	tx, commit, rollback, hostErr := resolveORMWriteTx(ctx, db, modCtx, input.TxID)
	if hostErr != nil {
		return ORMWriteOutput{}, hostErr
	}
	defer rollback()

	if hostErr := checkDynamicLinkTargets(ctx, tx, modCtx, md, input.Record); hostErr != nil {
		return ORMWriteOutput{}, hostErr
	}

	oldData, hostErr := fetchRowForAuditBeforeWrite(ctx, tx, modCtx, input.Model, md, pkCol, input.ID)
	if hostErr != nil {
		return ORMWriteOutput{}, hostErr
	}

	updated, hostErr := writeOneRecordTx(ctx, tx, modCtx, md, input.Model, pkCol, input.ID, record, input.ExpectedEtag)
	if hostErr != nil {
		return ORMWriteOutput{}, hostErr
	}

	if hostErr := maintainTreePathOnWrite(ctx, tx, md, pkCol, input.ID, input.Record); hostErr != nil {
		return ORMWriteOutput{}, hostErr
	}
	if hostErr := recomputeAfterWrite(ctx, tx, r, modCtx, input.Model, md, changedFieldNames(input.Record), updated); hostErr != nil {
		return ORMWriteOutput{}, hostErr
	}
	if hostErr := runConstraintHook(ctx, r, modCtx, input.Model, "write", updated); hostErr != nil {
		return ORMWriteOutput{}, hostErr
	}
	if hostErr := writeAuditLogEntry(ctx, tx, modCtx, input.Model, md, "UPDATE", oldData, updated); hostErr != nil {
		return ORMWriteOutput{}, hostErr
	}

	if err := emitRecordEvent(ctx, insertClient, tx, modCtx, "orm.record.updated", input.Model, updated); err != nil {
		return ORMWriteOutput{}, &abi.HostError{Code: abi.ErrCodeUnavailable, Message: err.Error(), Retry: true}
	}

	if err := commit(); err != nil {
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
	if hostErr := validateDynamicLinkPairs(md, input.Record); hostErr != nil {
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

	tx, commit, rollback, hostErr := resolveORMWriteTx(ctx, db, modCtx, input.TxID)
	if hostErr != nil {
		return ExecResult{}, hostErr
	}
	defer rollback()

	if hostErr := checkDynamicLinkTargets(ctx, tx, modCtx, md, input.Record); hostErr != nil {
		return ExecResult{}, hostErr
	}

	result, hostErr := writeManyIDsTx(ctx, tx, r, insertClient, modCtx, md, pkCol, input.Model, input.IDs, record)
	if hostErr != nil {
		return ExecResult{}, hostErr
	}

	if err := commit(); err != nil {
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
	if hostErr := validateDynamicLinkPairs(md, input.Record); hostErr != nil {
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

	tx, commit, rollback, hostErr := resolveORMWriteTx(ctx, db, modCtx, input.TxID)
	if hostErr != nil {
		return ExecResult{}, hostErr
	}
	defer rollback()

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

	if hostErr := checkDynamicLinkTargets(ctx, tx, modCtx, md, input.Record); hostErr != nil {
		return ExecResult{}, hostErr
	}

	result, hostErr := writeManyIDsTx(ctx, tx, r, insertClient, modCtx, md, pkCol, input.Model, ids, record)
	if hostErr != nil {
		return ExecResult{}, hostErr
	}

	if err := commit(); err != nil {
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
// internally; otherwise shares unlinkManyIDsTx with the SQL-backed loop
// below. A missing ID aborts the whole transaction (orm.not_found),
// matching writeManyIDsTx's own all-or-nothing semantics for WriteMany/
// WriteWhere.
func ORMUnlink(ctx context.Context, r *Runtime, db *sql.DB, insertClient *river.Client[*sql.Tx], cacheClient *cache.Client, modCtx *ModuleContext, input ORMUnlinkInput) (ExecResult, *abi.HostError) {
	if !modCtx.Capabilities().Has(abi.CapDBWrite) {
		return ExecResult{}, abi.CapabilityDenied("db.write")
	}

	md, ok := resolveModel(modCtx, input.Model)
	if !ok {
		return ExecResult{}, &abi.HostError{Code: abi.ErrCodeModelNotFound, Message: "model " + input.Model + " is not declared by this module"}
	}

	if md.Backend == model.BackendTransient {
		return transientUnlink(ctx, cacheClient, modCtx, input.Model, input.IDs)
	}

	pkCol, ok := primaryKeyColumn(md)
	if !ok {
		return ExecResult{}, &abi.HostError{Code: abi.ErrCodeModelNotFound, Message: "model " + input.Model + " declares no primary key field"}
	}

	tx, commit, rollback, hostErr := resolveORMWriteTx(ctx, db, modCtx, input.TxID)
	if hostErr != nil {
		return ExecResult{}, hostErr
	}
	defer rollback()

	result, hostErr := unlinkManyIDsTx(ctx, tx, r, insertClient, modCtx, md, pkCol, input.Model, input.IDs)
	if hostErr != nil {
		return ExecResult{}, hostErr
	}

	if err := commit(); err != nil {
		return ExecResult{}, &abi.HostError{Code: abi.ErrCodeCommitFailed, Message: err.Error()}
	}

	return result, nil
}

// unlinkManyIDsTx deletes (or soft-deletes) every id on tx, running the
// per-record OnDelete constraint hook/audit/orm.record.deleted event
// sequence for each — the same shared-loop shape writeManyIDsTx uses for
// WriteMany/WriteWhere. Fetches each row before deleting it (fetchRowByPK,
// goerp#377) so the constraint hook can run against it, and — per
// go-sdk-reference.md §22 — before either the FK check or the delete SQL
// itself, unlike OnCreate/OnWrite's after-the-write placement (this
// file's own package doc comment explains why those two differ).
func unlinkManyIDsTx(ctx context.Context, tx *sql.Tx, r *Runtime, insertClient *river.Client[*sql.Tx], modCtx *ModuleContext, md model.ModelDeclaration, pkCol, qualifiedModel string, ids []string) (ExecResult, *abi.HostError) {
	table := quoteIdentORM(tableNameForORM(md))
	pkColQuoted := quoteIdentORM(pkCol)

	affected := make([]string, 0, len(ids))
	for _, id := range ids {
		existing, hostErr := fetchRowByPK(ctx, tx, md, pkCol, id)
		if hostErr != nil {
			return ExecResult{}, hostErr
		}
		if hostErr := runConstraintHook(ctx, r, modCtx, qualifiedModel, "delete", existing); hostErr != nil {
			return ExecResult{}, hostErr
		}

		var deletedID string
		var err error
		if hasField(md, "deleted_at") {
			deleteSQL := fmt.Sprintf("UPDATE %s SET %s = NOW() WHERE %s = $1 AND %s IS NULL RETURNING %s",
				table, quoteIdentORM("deleted_at"), pkColQuoted, quoteIdentORM("deleted_at"), pkColQuoted)
			err = tx.QueryRowContext(ctx, deleteSQL, id).Scan(&deletedID)
		} else {
			deleteSQL := fmt.Sprintf("DELETE FROM %s WHERE %s = $1 RETURNING %s", table, pkColQuoted, pkColQuoted)
			err = tx.QueryRowContext(ctx, deleteSQL, id).Scan(&deletedID)
		}
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ExecResult{}, &abi.HostError{Code: abi.ErrCodeNotFound, Message: "record not found"}
			}
			return ExecResult{}, translateWriteError(err, md)
		}

		if hostErr := recomputeParentsAfterChildUnlink(ctx, tx, r, modCtx, qualifiedModel, existing); hostErr != nil {
			return ExecResult{}, hostErr
		}
		if hostErr := writeAuditLogEntry(ctx, tx, modCtx, qualifiedModel, md, "DELETE", existing, nil); hostErr != nil {
			return ExecResult{}, hostErr
		}
		if err := emitRecordEvent(ctx, insertClient, tx, modCtx, "orm.record.deleted", qualifiedModel, map[string]any{"id": deletedID}); err != nil {
			return ExecResult{}, &abi.HostError{Code: abi.ErrCodeUnavailable, Message: err.Error(), Retry: true}
		}

		affected = append(affected, deletedID)
	}
	return ExecResult{Count: len(affected), IDs: affected}, nil
}

// resolveORMWriteTx is resolveORMReadTx's (host_orm.go) write-side
// counterpart: txID's borrowed transaction when non-empty, or a freshly
// opened tenant-scoped one otherwise. commit and rollback are both
// no-ops for a borrowed transaction — a host.orm write call must never
// commit or roll back a transaction it didn't open itself, since that's
// the caller's own host.db.commit/rollback responsibility. For an owned
// transaction, commit/rollback are tx.Commit/tx.Rollback directly, so
// existing call sites keep their own "defer rollback, explicit commit on
// success" shape unchanged.
func resolveORMWriteTx(ctx context.Context, db *sql.DB, modCtx *ModuleContext, txID string) (tx *sql.Tx, commit func() error, rollback func(), hostErr *abi.HostError) {
	if txID != "" {
		tx, ok := modCtx.Transaction(txID)
		if !ok {
			return nil, nil, nil, &abi.HostError{Code: abi.ErrCodeTransactionNotFound, Message: "transaction ID does not exist or has expired"}
		}
		return tx, func() error { return nil }, func() {}, nil
	}
	tx, err := beginTenantScopedWrite(ctx, db, modCtx)
	if err != nil {
		return nil, nil, nil, &abi.HostError{Code: abi.ErrCodeUnavailable, Message: err.Error(), Retry: true}
	}
	return tx, tx.Commit, func() { _ = tx.Rollback() }, nil
}

// beginTenantScopedWrite is beginTenantScopedRead without ReadOnly — same
// session-var scoping so RLS applies automatically to writes too.
func beginTenantScopedWrite(ctx context.Context, db *sql.DB, modCtx *ModuleContext) (*sql.Tx, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	if err := applyTenantScope(ctx, tx, modCtx); err != nil {
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
//
// The single chokepoint for write-side field security (manifest-spec.md
// §8a, auth-internals.md §12 "Write behaviours") — every write path
// (create, create_batch, first_or_create, write, write_many,
// write_where) funnels through here via createOneRecordTx/
// writeOneRecordTx. A field with a WritePermission the caller doesn't
// satisfy is rejected outright (OnDeniedWrite Reject, the default) or
// silently absent from cols/args (Ignore) — either way before any SQL
// runs.
func buildAssignment(modCtx *ModuleContext, qualifiedModel string, md model.ModelDeclaration, record map[string]any) (cols []string, args []any, hostErr *abi.HostError) {
	fields := make(map[string]model.FieldDef, len(md.Fields))
	for _, f := range md.Fields {
		fields[f.Name] = f.Def
		if f.Def.IsTree {
			// {field}_path is an engine-managed companion column
			// (schema.toAtlasTable, goerp#379) — never declared in
			// md.Fields itself, but a legitimate column
			// injectTreePathOnCreate/maintainTreePathOnWrite write to.
			fields[f.Name+"_path"] = model.FieldDef{}
		}
	}

	fieldSecReg := modCtx.FieldSecRegistry()
	permReg := modCtx.PermissionRegistry()

	for k, v := range record {
		def, known := fields[k]
		if !known {
			return nil, nil, &abi.HostError{Code: abi.ErrCodeValidationFailed, Message: "unknown field " + k, Details: map[string]any{"field": k}}
		}
		if def.IsComputed {
			return nil, nil, &abi.HostError{Code: abi.ErrCodeFieldNotWritable, Message: "field " + k + " is computed and cannot be written directly", Details: map[string]any{"field": k}}
		}
		if def.IsReadonly {
			return nil, nil, &abi.HostError{Code: abi.ErrCodeFieldNotWritable, Message: "field " + k + " is readonly and cannot be written directly", Details: map[string]any{"field": k}}
		}
		if def.Kind == model.KindOne2Many {
			return nil, nil, &abi.HostError{Code: abi.ErrCodeFieldNotWritable, Message: "field " + k + " is a One2Many relation and cannot be written directly", Details: map[string]any{"field": k}}
		}

		if fieldSecReg != nil {
			if rule, ok := fieldSecReg.Rule(qualifiedModel, k); ok && rule.WritePermission != "" && !callerHasPermission(modCtx, permReg, rule.WritePermission) {
				if rule.OnDeniedWrite == fieldsec.Ignore {
					continue
				}
				return nil, nil, &abi.HostError{Code: abi.ErrCodeFieldWriteDenied, Message: "field " + k + " requires permission " + rule.WritePermission, Details: map[string]any{"field": k}}
			}
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
func createOneRecordTx(ctx context.Context, tx *sql.Tx, modCtx *ModuleContext, md model.ModelDeclaration, qualifiedModel string, record map[string]any, onConflict *OnConflictOption) (map[string]any, bool, *abi.HostError) {
	cols, args, hostErr := buildAssignment(modCtx, qualifiedModel, md, record)
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
		targetCols, hostErr := validateOnConflictTarget(md, qualifiedModel, onConflict.Fields, "on_conflict.fields")
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
// full-table-scan fallback for conflict detection. paramName is the
// caller's own wire field name (e.g. "on_conflict.fields", "unique_vals"),
// used in error messages.
func validateOnConflictTarget(md model.ModelDeclaration, qualifiedModel string, fields []string, paramName string) ([]string, *abi.HostError) {
	if len(fields) == 0 {
		return nil, &abi.HostError{Code: abi.ErrCodeConflictTargetInvalid, Message: paramName + " must not be empty"}
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
	return nil, &abi.HostError{Code: abi.ErrCodeConflictTargetInvalid, Message: paramName + " " + strings.Join(fields, ", ") + " does not match any declared unique index on " + qualifiedModel}
}

// writeOneRecordTx updates the row identified by id on tx — the shared
// core of ORMWrite/ORMWriteMany/ORMWriteWhere. expectedEtag == "" skips
// the etag-scoped WHERE clause entirely (the bulk callers' "no etag
// check" semantics); a non-empty expectedEtag adds it, matching single
// write's optimistic-lock behavior.
func writeOneRecordTx(ctx context.Context, tx *sql.Tx, modCtx *ModuleContext, md model.ModelDeclaration, qualifiedModel, pkCol, id string, record map[string]any, expectedEtag string) (map[string]any, *abi.HostError) {
	sets, args, hostErr := buildAssignment(modCtx, qualifiedModel, md, record)
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
		oldData, hostErr := fetchRowForAuditBeforeWrite(ctx, tx, modCtx, qualifiedModel, md, pkCol, id)
		if hostErr != nil {
			return ExecResult{}, hostErr
		}
		updated, hostErr := writeOneRecordTx(ctx, tx, modCtx, md, qualifiedModel, pkCol, id, record, "")
		if hostErr != nil {
			return ExecResult{}, hostErr
		}
		if hostErr := maintainTreePathOnWrite(ctx, tx, md, pkCol, id, record); hostErr != nil {
			return ExecResult{}, hostErr
		}
		if hostErr := recomputeAfterWrite(ctx, tx, r, modCtx, qualifiedModel, md, changedFields, updated); hostErr != nil {
			return ExecResult{}, hostErr
		}
		if hostErr := runConstraintHook(ctx, r, modCtx, qualifiedModel, "write", updated); hostErr != nil {
			return ExecResult{}, hostErr
		}
		if hostErr := writeAuditLogEntry(ctx, tx, modCtx, qualifiedModel, md, "UPDATE", oldData, updated); hostErr != nil {
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

		if dep.ViaChildFKField != "" {
			// One2Many hop: row (the child just written) names its one
			// parent directly via ViaChildFKField — no query needed.
			if hostErr := recomputeParentViaChild(ctx, tx, r, modCtx, dep, row); hostErr != nil {
				return hostErr
			}
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

// recomputeParentViaChild resolves and recomputes dep's single parent
// record directly from row's own inverse-FK column value — the One2Many
// hop's counterpart to the Many2One-hop branch above, with no query
// needed since a child row always names its one parent directly
// (go-sdk-reference.md §22 "One2Many" / "Computed field recomputation").
// If row's own inverse-FK value changes on write (reparenting to a
// different parent), only the new parent recomputes here — the old
// parent's dependents are not recomputed to reflect the child's removal.
func recomputeParentViaChild(ctx context.Context, tx *sql.Tx, r *Runtime, modCtx *ModuleContext, dep computed.Dependent, row map[string]any) *abi.HostError {
	depPK, ok := primaryKeyColumn(dep.ModelDecl)
	if !ok {
		return nil
	}
	parentID, ok := row[dep.ViaChildFKField]
	if !ok || parentID == nil {
		// Child not (yet) linked to any parent — nothing to recompute.
		return nil
	}

	depRow, hostErr := fetchRowByPK(ctx, tx, dep.ModelDecl, depPK, parentID)
	if hostErr != nil {
		return hostErr
	}
	value, hostErr := invokeCompute(ctx, r, modCtx, dep, depRow)
	if hostErr != nil {
		return hostErr
	}
	return applyComputedValue(ctx, tx, dep.ModelDecl, depPK, parentID, dep.Field, value)
}

// recomputeParentsAfterChildUnlink recomputes every parent computed field
// reached through a One2Many relationship when a child row is deleted
// (go-sdk-reference.md §22 "One2Many" / "Computed field recomputation").
// existingRow is the child's full pre-delete snapshot. Deliberately uses
// LookupViaChild rather than Lookup: the child's own same-record and
// Many2One-hop dependents, if any, are never recomputed here since the
// row they'd apply to no longer exists.
func recomputeParentsAfterChildUnlink(ctx context.Context, tx *sql.Tx, r *Runtime, modCtx *ModuleContext, qualifiedChildModel string, existingRow map[string]any) *abi.HostError {
	idx := modCtx.ComputedIndex()
	if idx == nil {
		return nil
	}
	for _, dep := range idx.LookupViaChild(qualifiedChildModel, changedFieldNames(existingRow)) {
		if hostErr := recomputeParentViaChild(ctx, tx, r, modCtx, dep, existingRow); hostErr != nil {
			return hostErr
		}
	}
	return nil
}

// auditLogTableName is the engine-owned table CreateEngineTables
// provisions per tenant (internal/engine/tenant/provision/activities.go,
// goerp#363) — never a declared model, so it's a fixed name rather than
// something resolveModel ever sees.
const auditLogTableName = "audit_log"

// writeAuditLogEntry inserts one audit_log row if qualifiedModel is
// declared in its owning module's own audited_tables[] (manifest-spec.md
// §19 "Audited Tables"); a no-op otherwise, and also a no-op if modCtx
// carries no DataAuditRegistry at all (mirrors recomputeAfterWrite's own
// nil-ComputedIndex guard — a test fixture that never wires one). This
// only ever covers writes that go through host.orm: the only write path
// any module has today, since host.db exposes no raw query/exec to WASM
// modules, so "every INSERT/UPDATE/DELETE" is fully satisfied here, not
// a partial mechanism. oldData/newData pass as nil for create's/delete's
// absent half respectively, stored as SQL NULL. Runs inside tx, so a
// later failure in the same request's write rolls the audit entry back
// too — no lost or orphaned audit entries on partial failure.
func writeAuditLogEntry(ctx context.Context, tx *sql.Tx, modCtx *ModuleContext, qualifiedModel string, md model.ModelDeclaration, operation string, oldData, newData map[string]any) *abi.HostError {
	reg := modCtx.DataAuditRegistry()
	if reg == nil {
		return nil
	}
	excludeCols, audited := reg.Lookup(qualifiedModel)
	if !audited {
		return nil
	}

	pkCol, ok := primaryKeyColumn(md)
	if !ok {
		return nil
	}
	recordID := oldData[pkCol]
	if newData != nil {
		recordID = newData[pkCol]
	}

	if err := insertAuditLogRow(ctx, tx, modCtx, tableNameForORM(md), recordID, operation, excludeCols, oldData, newData); err != nil {
		return &abi.HostError{Code: abi.ErrCodeUnavailable, Message: err.Error()}
	}
	return nil
}

// insertAuditLogRow marshals oldData/newData (excludeCols stripped) and
// inserts one audit_log row. Shared by writeAuditLogEntry (host.orm's
// single-record write path) and writeOneExecAuditEntry
// (host_db_exec_audit.go's raw-SQL path) so the two can't drift apart.
func insertAuditLogRow(ctx context.Context, tx *sql.Tx, modCtx *ModuleContext, table string, recordID any, operation string, excludeCols map[string]bool, oldData, newData map[string]any) error {
	oldJSON, err := auditJSON(oldData, excludeCols)
	if err != nil {
		return err
	}
	newJSON, err := auditJSON(newData, excludeCols)
	if err != nil {
		return err
	}

	sqlStr := fmt.Sprintf(`INSERT INTO %s (table_name, record_id, operation, old_data, new_data, changed_by, request_id, trace_id)
		VALUES ($1, $2, $3, $4, $5, NULLIF($6, '')::uuid, $7, $8)`, quoteIdentORM(auditLogTableName))
	if _, err := tx.ExecContext(ctx, sqlStr, table, recordID, operation, oldJSON, newJSON, modCtx.UserID, modCtx.RequestID, modCtx.TraceID); err != nil {
		return fmt.Errorf("insert audit_log row: %w", err)
	}
	return nil
}

// auditLogEntry is one row insertAuditLogRows writes to audit_log —
// oldData/newData in the same shape insertAuditLogRow itself takes.
type auditLogEntry struct {
	RecordID any
	OldData  map[string]any
	NewData  map[string]any
}

// maxAuditWriteChunkParams caps how many bound parameters
// insertAuditLogRows builds into a single multi-row INSERT — comfortably
// under Postgres's 65535-bound-parameters-per-statement limit, with
// headroom for its own fixed 8-params-per-row shape.
// captureRowsBeforeExecBatch (host_db_exec_audit.go) chunks its own
// batched pre-read against a separate constant of the same value
// (maxAuditPreReadChunkWeight) — the two happen to share a value but
// bound structurally different things (an exact per-row parameter count
// here vs. a conservative per-row weight estimate there), so tuning one
// is never assumed to be safe for the other.
const maxAuditWriteChunkParams = 5000

// insertAuditLogRows writes entries to audit_log via one multi-row INSERT
// per chunk, instead of insertAuditLogRow's own one-round-trip-per-row
// form — table, operation, excludeCols, and modCtx's own changed_by/
// request_id/trace_id are shared across every row, since every call site
// (writeAuditForExec's own INSERT branch, writeExecAuditEntries) already
// writes audit rows for one table/operation within one request. A
// single-entry chunk reuses insertAuditLogRow's own SQL text unchanged,
// rather than building a one-row VALUES list a different way.
func insertAuditLogRows(ctx context.Context, tx *sql.Tx, modCtx *ModuleContext, table, operation string, excludeCols map[string]bool, entries []auditLogEntry) error {
	const paramsPerRow = 8
	rowsPerChunk := maxAuditWriteChunkParams / paramsPerRow

	for start := 0; start < len(entries); start += rowsPerChunk {
		chunk := entries[start:min(start+rowsPerChunk, len(entries))]

		if len(chunk) == 1 {
			e := chunk[0]
			if err := insertAuditLogRow(ctx, tx, modCtx, table, e.RecordID, operation, excludeCols, e.OldData, e.NewData); err != nil {
				return err
			}
			continue
		}

		placeholders := make([]string, len(chunk))
		args := make([]any, 0, len(chunk)*paramsPerRow)
		for i, e := range chunk {
			oldJSON, err := auditJSON(e.OldData, excludeCols)
			if err != nil {
				return err
			}
			newJSON, err := auditJSON(e.NewData, excludeCols)
			if err != nil {
				return err
			}
			base := i * paramsPerRow
			placeholders[i] = fmt.Sprintf("($%d, $%d, $%d, $%d, $%d, NULLIF($%d, '')::uuid, $%d, $%d)",
				base+1, base+2, base+3, base+4, base+5, base+6, base+7, base+8)
			args = append(args, table, e.RecordID, operation, oldJSON, newJSON, modCtx.UserID, modCtx.RequestID, modCtx.TraceID)
		}

		sqlStr := fmt.Sprintf(`INSERT INTO %s (table_name, record_id, operation, old_data, new_data, changed_by, request_id, trace_id)
			VALUES %s`, quoteIdentORM(auditLogTableName), strings.Join(placeholders, ", "))
		if _, err := tx.ExecContext(ctx, sqlStr, args...); err != nil {
			return fmt.Errorf("insert audit_log rows: %w", err)
		}
	}
	return nil
}

// fetchRowForAuditBeforeWrite fetches qualifiedModel's row by pkValue
// before an UPDATE runs, so writeAuditLogEntry has a real old_data
// snapshot to record — but only when the table is actually audited, so
// the common non-audited case pays no extra round trip. Returns a nil
// map (not an error) when the table isn't audited, modCtx carries no
// DataAuditRegistry at all, or the row simply doesn't exist (ID/etag
// mismatch) — that last case is deliberately swallowed rather than
// surfaced here, since the caller's own subsequent writeOneRecordTx
// already produces the correct, more specific orm.not_found vs.
// orm.etag_mismatch diagnosis (diagnoseZeroRowWrite) and this helper
// must not shadow that with a generic error first.
func fetchRowForAuditBeforeWrite(ctx context.Context, tx *sql.Tx, modCtx *ModuleContext, qualifiedModel string, md model.ModelDeclaration, pkCol, pkValue string) (map[string]any, *abi.HostError) {
	reg := modCtx.DataAuditRegistry()
	if reg == nil {
		return nil, nil
	}
	if _, audited := reg.Lookup(qualifiedModel); !audited {
		return nil, nil
	}
	row, hostErr := fetchRowByPK(ctx, tx, md, pkCol, pkValue)
	if hostErr != nil {
		if hostErr.Code == abi.ErrCodeNotFound {
			return nil, nil
		}
		return nil, hostErr
	}
	return row, nil
}

// auditJSON filters excludeCols out of data and marshals the rest as
// JSONB. A nil data map (create's absent old_data, delete's absent
// new_data) returns a real Go nil so the column stores SQL NULL, not
// json.Marshal(nil)'s literal JSON "null".
func auditJSON(data map[string]any, excludeCols map[string]bool) (any, error) {
	if data == nil {
		return nil, nil
	}
	filtered := make(map[string]any, len(data))
	for k, v := range data {
		if excludeCols[k] {
			continue
		}
		filtered[k] = v
	}
	return json.Marshal(filtered)
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
// (single-hop only, no cascading, per the AC). If the table carries the
// engine's update_etag() trigger (internal/engine/schema/etag_trigger.go,
// installed for any table in a module's audited_tables[]), that BEFORE
// UPDATE trigger would otherwise rotate etag/updated_at on this UPDATE
// regardless of which column is in its SET clause — app.skip_etag_trigger
// tells it to skip, the same set_config-based session-variable mechanism
// applyTenantScope uses for app.current_user_id etc. (tenant_scope.go).
// Scoped SET LOCAL (is_local=true), so it can't leak past this
// transaction or suppress etag rotation on any other write in it.
func applyComputedValue(ctx context.Context, tx *sql.Tx, md model.ModelDeclaration, pkCol string, pkValue any, field string, value any) *abi.HostError {
	table := quoteIdentORM(tableNameForORM(md))

	if _, err := tx.ExecContext(ctx, "SELECT set_config('app.skip_etag_trigger', 'true', true)"); err != nil {
		return &abi.HostError{Code: abi.ErrCodeUnavailable, Message: err.Error()}
	}

	sqlStr := fmt.Sprintf("UPDATE %s SET %s = $1 WHERE %s = $2", table, quoteIdentORM(field), quoteIdentORM(pkCol))
	if _, err := tx.ExecContext(ctx, sqlStr, value, pkValue); err != nil {
		return translateWriteError(err, md)
	}

	if _, err := tx.ExecContext(ctx, "SELECT set_config('app.skip_etag_trigger', 'false', true)"); err != nil {
		return &abi.HostError{Code: abi.ErrCodeUnavailable, Message: err.Error()}
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
