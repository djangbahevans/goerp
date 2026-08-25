package wasm

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"maps"
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

// This file holds host.orm's write half — create/write/unlink — the
// goerp#343 core single-record path. Batch variants (create_batch,
// first_or_create, write_many, write_where), OnConflictIgnore/
// OnConflictUpdate, computed-field recompute (Store(true)/.Depends() has
// no SDK primitive yet), orm.RegisterConstraint/OnDelete hooks (no SDK
// primitive yet), DynamicLink/​.Tree() validation (no such field kinds
// exist yet), and audit logging (goerp#363, tracked separately — no
// audited_tables trigger mechanism exists anywhere in the engine despite
// this ticket's own original text assuming one did) are all explicitly
// out of scope for this file.

type ormCreateInput struct {
	Model  string         `msgpack:"model"`
	Record map[string]any `msgpack:"record"`
}

type ormCreateOutput struct {
	Record map[string]any `msgpack:"record"`
}

type ormWriteInput struct {
	Model        string         `msgpack:"model"`
	ID           string         `msgpack:"id"`
	Record       map[string]any `msgpack:"record"`
	ExpectedEtag string         `msgpack:"expected_etag,omitempty"`
}

type ormWriteOutput struct {
	Record map[string]any `msgpack:"record"`
}

type ormUnlinkInput struct {
	Model string `msgpack:"model"`
	ID    string `msgpack:"id"`
}

type ormUnlinkOutput struct {
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
		var input ormCreateInput
		if err := msgpack.Unmarshal(inputBytes, &input); err != nil {
			return abi.EncodeHostError(ctx, m, allocate, abi.DeserializeError(err))
		}

		out, hostErr := ORMCreate(ctx, db, insertClient, cacheClient, modCtx, input)
		if hostErr != nil {
			return abi.EncodeHostError(ctx, m, allocate, hostErr)
		}
		return abi.WriteToModule(ctx, m, allocate, out)
	}
}

// ORMCreate is host.orm create's plain-Go core — see ORMSearch's doc
// comment (host_orm.go) for the shared-entry-point rationale. Branches to
// transientCreate (host_orm_transient.go) for Transient-backed models
// internally.
func ORMCreate(ctx context.Context, db *sql.DB, insertClient *river.Client[*sql.Tx], cacheClient *cache.Client, modCtx *ModuleContext, input ormCreateInput) (ormCreateOutput, *abi.HostError) {
	if !modCtx.Capabilities().Has(abi.CapDBWrite) {
		return ormCreateOutput{}, abi.CapabilityDenied("db.write")
	}

	md, ok := resolveModel(modCtx, input.Model)
	if !ok {
		return ormCreateOutput{}, &abi.HostError{Code: abi.ErrCodeModelNotFound, Message: "model " + input.Model + " is not declared by this module"}
	}

	record := make(map[string]any, len(input.Record))
	maps.Copy(record, input.Record)

	if hostErr := validateRequired(md, record, true); hostErr != nil {
		return ormCreateOutput{}, hostErr
	}

	if md.Backend == model.BackendTransient {
		return transientCreate(ctx, cacheClient, modCtx, md, input.Model, record)
	}

	tx, err := beginTenantScopedWrite(ctx, db, modCtx)
	if err != nil {
		return ormCreateOutput{}, &abi.HostError{Code: abi.ErrCodeUnavailable, Message: err.Error(), Retry: true}
	}
	defer func() { _ = tx.Rollback() }()

	if hostErr := acquireSequenceFields(ctx, tx, modCtx.TenantSlug, input.Model, md, record); hostErr != nil {
		return ormCreateOutput{}, hostErr
	}

	cols, args, hostErr := buildAssignment(md, record)
	if hostErr != nil {
		return ormCreateOutput{}, hostErr
	}
	if len(cols) == 0 {
		return ormCreateOutput{}, &abi.HostError{Code: abi.ErrCodeValidationFailed, Message: "record has no fields to insert"}
	}
	placeholders := make([]string, len(args))
	for i := range args {
		placeholders[i] = fmt.Sprintf("$%d", i+1)
	}

	table := quoteIdentORM(tableNameForORM(md))
	insertSQL := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s) RETURNING *",
		table, strings.Join(cols, ", "), strings.Join(placeholders, ", "))

	rows, err := tx.QueryContext(ctx, insertSQL, args...)
	if err != nil {
		return ormCreateOutput{}, translateWriteError(err, md)
	}
	created, err := scanRowsToMaps(rows)
	if err != nil {
		return ormCreateOutput{}, &abi.HostError{Code: abi.ErrCodeUnavailable, Message: err.Error()}
	}
	if len(created) != 1 {
		return ormCreateOutput{}, &abi.HostError{Code: abi.ErrCodeUnavailable, Message: "insert did not return exactly one row"}
	}

	if err := emitRecordEvent(ctx, insertClient, tx, modCtx, "orm.record.created", input.Model, created[0]); err != nil {
		return ormCreateOutput{}, &abi.HostError{Code: abi.ErrCodeUnavailable, Message: err.Error(), Retry: true}
	}

	if err := tx.Commit(); err != nil {
		return ormCreateOutput{}, &abi.HostError{Code: abi.ErrCodeCommitFailed, Message: err.Error()}
	}

	return ormCreateOutput{Record: created[0]}, nil
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
		var input ormWriteInput
		if err := msgpack.Unmarshal(inputBytes, &input); err != nil {
			return abi.EncodeHostError(ctx, m, allocate, abi.DeserializeError(err))
		}

		out, hostErr := ORMWrite(ctx, db, insertClient, cacheClient, modCtx, input)
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
// models internally.
func ORMWrite(ctx context.Context, db *sql.DB, insertClient *river.Client[*sql.Tx], cacheClient *cache.Client, modCtx *ModuleContext, input ormWriteInput) (ormWriteOutput, *abi.HostError) {
	if !modCtx.Capabilities().Has(abi.CapDBWrite) {
		return ormWriteOutput{}, abi.CapabilityDenied("db.write")
	}

	md, ok := resolveModel(modCtx, input.Model)
	if !ok {
		return ormWriteOutput{}, &abi.HostError{Code: abi.ErrCodeModelNotFound, Message: "model " + input.Model + " is not declared by this module"}
	}

	if hostErr := validateRequired(md, input.Record, false); hostErr != nil {
		return ormWriteOutput{}, hostErr
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
		return ormWriteOutput{}, &abi.HostError{Code: abi.ErrCodeModelNotFound, Message: "model " + input.Model + " declares no primary key field"}
	}

	tx, err := beginTenantScopedWrite(ctx, db, modCtx)
	if err != nil {
		return ormWriteOutput{}, &abi.HostError{Code: abi.ErrCodeUnavailable, Message: err.Error(), Retry: true}
	}
	defer func() { _ = tx.Rollback() }()

	sets, args, hostErr := buildAssignment(md, record)
	if hostErr != nil {
		return ormWriteOutput{}, hostErr
	}
	if len(sets) == 0 {
		return ormWriteOutput{}, &abi.HostError{Code: abi.ErrCodeValidationFailed, Message: "record has no fields to update"}
	}

	table := quoteIdentORM(tableNameForORM(md))
	pkColQuoted := quoteIdentORM(pkCol)
	setClauses := make([]string, len(sets))
	for i, col := range sets {
		setClauses[i] = col + " = $" + fmt.Sprint(i+1)
	}

	args = append(args, input.ID)
	whereClause := fmt.Sprintf("%s = $%d", pkColQuoted, len(args))
	if input.ExpectedEtag != "" && hasField(md, "etag") {
		args = append(args, input.ExpectedEtag)
		whereClause += fmt.Sprintf(" AND %s = $%d", quoteIdentORM("etag"), len(args))
	}

	updateSQL := fmt.Sprintf("UPDATE %s SET %s WHERE %s RETURNING *",
		table, strings.Join(setClauses, ", "), whereClause)

	rows, err := tx.QueryContext(ctx, updateSQL, args...)
	if err != nil {
		return ormWriteOutput{}, translateWriteError(err, md)
	}
	updated, err := scanRowsToMaps(rows)
	if err != nil {
		return ormWriteOutput{}, &abi.HostError{Code: abi.ErrCodeUnavailable, Message: err.Error()}
	}

	if len(updated) == 0 {
		return ormWriteOutput{}, diagnoseZeroRowWrite(ctx, tx, table, pkColQuoted, input.ID, input.ExpectedEtag)
	}

	if err := emitRecordEvent(ctx, insertClient, tx, modCtx, "orm.record.updated", input.Model, updated[0]); err != nil {
		return ormWriteOutput{}, &abi.HostError{Code: abi.ErrCodeUnavailable, Message: err.Error(), Retry: true}
	}

	if err := tx.Commit(); err != nil {
		return ormWriteOutput{}, &abi.HostError{Code: abi.ErrCodeCommitFailed, Message: err.Error()}
	}

	return ormWriteOutput{Record: updated[0]}, nil
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
		var input ormUnlinkInput
		if err := msgpack.Unmarshal(inputBytes, &input); err != nil {
			return abi.EncodeHostError(ctx, m, allocate, abi.DeserializeError(err))
		}

		out, hostErr := ORMUnlink(ctx, db, insertClient, cacheClient, modCtx, input)
		if hostErr != nil {
			return abi.EncodeHostError(ctx, m, allocate, hostErr)
		}
		return abi.WriteToModule(ctx, m, allocate, out)
	}
}

// ORMUnlink is host.orm unlink's plain-Go core — see ORMSearch's doc
// comment (host_orm.go) for the shared-entry-point rationale. Branches to
// transientUnlink (host_orm_transient.go) for Transient-backed models
// internally.
func ORMUnlink(ctx context.Context, db *sql.DB, insertClient *river.Client[*sql.Tx], cacheClient *cache.Client, modCtx *ModuleContext, input ormUnlinkInput) (ormUnlinkOutput, *abi.HostError) {
	if !modCtx.Capabilities().Has(abi.CapDBWrite) {
		return ormUnlinkOutput{}, abi.CapabilityDenied("db.write")
	}

	md, ok := resolveModel(modCtx, input.Model)
	if !ok {
		return ormUnlinkOutput{}, &abi.HostError{Code: abi.ErrCodeModelNotFound, Message: "model " + input.Model + " is not declared by this module"}
	}

	if md.Backend == model.BackendTransient {
		return transientUnlink(ctx, cacheClient, modCtx, input.Model, input.ID)
	}

	pkCol, ok := primaryKeyColumn(md)
	if !ok {
		return ormUnlinkOutput{}, &abi.HostError{Code: abi.ErrCodeModelNotFound, Message: "model " + input.Model + " declares no primary key field"}
	}

	tx, err := beginTenantScopedWrite(ctx, db, modCtx)
	if err != nil {
		return ormUnlinkOutput{}, &abi.HostError{Code: abi.ErrCodeUnavailable, Message: err.Error(), Retry: true}
	}
	defer func() { _ = tx.Rollback() }()

	table := quoteIdentORM(tableNameForORM(md))
	pkColQuoted := quoteIdentORM(pkCol)

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
			return ormUnlinkOutput{}, &abi.HostError{Code: abi.ErrCodeNotFound, Message: "record not found"}
		}
		return ormUnlinkOutput{}, translateWriteError(err, md)
	}

	if err := emitRecordEvent(ctx, insertClient, tx, modCtx, "orm.record.deleted", input.Model, map[string]any{"id": deletedID}); err != nil {
		return ormUnlinkOutput{}, &abi.HostError{Code: abi.ErrCodeUnavailable, Message: err.Error(), Retry: true}
	}

	if err := tx.Commit(); err != nil {
		return ormUnlinkOutput{}, &abi.HostError{Code: abi.ErrCodeCommitFailed, Message: err.Error()}
	}

	return ormUnlinkOutput{Deleted: true}, nil
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
// build from the returned columns/args.
func buildAssignment(md model.ModelDeclaration, record map[string]any) (cols []string, args []any, hostErr *abi.HostError) {
	known := make(map[string]bool, len(md.Fields))
	for _, f := range md.Fields {
		known[f.Name] = true
	}
	for k, v := range record {
		if !known[k] {
			return nil, nil, &abi.HostError{Code: abi.ErrCodeValidationFailed, Message: "unknown field " + k, Details: map[string]any{"field": k}}
		}
		cols = append(cols, quoteIdentORM(k))
		args = append(args, v)
	}
	return cols, args, nil
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
