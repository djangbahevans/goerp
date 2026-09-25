package wasm

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"uuid"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/djangbahevans/goerp/internal/engine/fieldsec"
	"github.com/djangbahevans/goerp/sdk/go/model"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/riverqueue/river"
	"github.com/tetratelabs/wazero/api"
	"github.com/vmihailenco/msgpack/v5"
)

type ORMMutateInput = abiv1.ORMMutateInput

type ORMMutateOutput = abiv1.ORMMutateOutput

func makeORMMutate(r *Runtime, db *sql.DB, insertClient *river.Client[*sql.Tx]) func(ctx context.Context, m api.Module, ptr, length uint32) uint64 {
	return func(ctx context.Context, m api.Module, ptr, length uint32) uint64 {
		inst := r.InstanceForModule(m)
		modCtx := inst.ModuleContext()
		allocate := inst.allocate

		inputBytes, err := abi.ReadFromModule(m.Memory(), ptr, length)
		if err != nil {
			return abi.EncodeHostError(ctx, m, allocate, abi.MemoryFault())
		}
		var input ORMMutateInput
		if err := msgpack.Unmarshal(inputBytes, &input); err != nil {
			return abi.EncodeHostError(ctx, m, allocate, abi.DeserializeError(err))
		}

		out, hostErr := ORMMutate(ctx, r, db, insertClient, modCtx, input)
		if hostErr != nil {
			return abi.EncodeHostError(ctx, m, allocate, hostErr)
		}
		return abi.WriteToModule(ctx, m, allocate, out)
	}
}

// ORMMutate is host.orm mutate's plain-Go core. The change is one guarded
// UPDATE, so concurrent callers serialise on the row lock and each guard
// sees the previous caller's committed value. An audited model takes that
// lock earlier, with SELECT ... FOR UPDATE, so old_data is the value the
// UPDATE replaced.
func ORMMutate(ctx context.Context, r *Runtime, db *sql.DB, insertClient *river.Client[*sql.Tx], modCtx *ModuleContext, input ORMMutateInput) (ORMMutateOutput, *abi.HostError) {
	if !modCtx.Capabilities().Has(abi.CapDBWrite) {
		return ORMMutateOutput{}, abi.CapabilityDenied("db.write")
	}

	md, ok := resolveModel(modCtx, input.Model)
	if !ok {
		return ORMMutateOutput{}, &abi.HostError{Code: abi.ErrCodeModelNotFound, Message: "model " + input.Model + " is not declared by this module"}
	}
	if md.Backend != "" {
		return ORMMutateOutput{}, &abi.HostError{Code: abi.ErrCodeValidationFailed, Message: "mutate requires a table-backed model, " + input.Model + " is " + string(md.Backend)}
	}
	pkCol, ok := primaryKeyColumn(md)
	if !ok {
		return ORMMutateOutput{}, &abi.HostError{Code: abi.ErrCodeModelNotFound, Message: "model " + input.Model + " declares no primary key field"}
	}

	plan, hostErr := planMutation(modCtx, input.Model, md, input.Ops)
	if hostErr != nil {
		return ORMMutateOutput{}, hostErr
	}

	guardFrag, guardArgs, hostErr := compileDomain(input.Guard)
	if hostErr != nil {
		return ORMMutateOutput{}, hostErr
	}

	tx, commit, rollback, hostErr := resolveORMWriteTx(ctx, db, modCtx, input.TxID)
	if hostErr != nil {
		return ORMMutateOutput{}, hostErr
	}
	defer rollback()

	table := quoteIdentORM(tableNameForORM(md))
	pkColQuoted := quoteIdentORM(pkCol)

	var oldData map[string]any
	if needsRowBeforeWrite(modCtx, input.Model, md) {
		oldData, hostErr = lockRowByPK(ctx, tx, md, pkCol, input.ID)
		if hostErr != nil {
			return ORMMutateOutput{}, hostErr
		}
	}

	args := guardArgs
	sets := make([]string, 0, len(plan.cols)+1)
	for i, col := range plan.cols {
		args = append(args, plan.deltas[i])
		sets = append(sets, fmt.Sprintf("%s = COALESCE(%s, 0) + $%d", col, col, len(args)))
	}
	if hasField(md, "etag") {
		args = append(args, uuid.NewV7().String())
		sets = append(sets, fmt.Sprintf("%s = $%d", quoteIdentORM("etag"), len(args)))
	}
	args = append(args, input.ID)
	where := fmt.Sprintf("%s = $%d", pkColQuoted, len(args))
	if input.Guard != "" {
		where += " AND (" + guardFrag + ")"
	}

	rows, err := tx.QueryContext(ctx, fmt.Sprintf("UPDATE %s SET %s WHERE %s RETURNING *", table, strings.Join(sets, ", "), where), args...)
	if err != nil {
		return ORMMutateOutput{}, translateMutateError(err, md)
	}
	updatedRows, err := scanRowsToMaps(rows)
	if err != nil {
		return ORMMutateOutput{}, translateMutateError(err, md)
	}
	if len(updatedRows) == 0 {
		return ORMMutateOutput{}, diagnoseZeroRowMutation(ctx, tx, table, pkColQuoted, input.ID)
	}
	updated := updatedRows[0]

	if hostErr := recomputeAfterWrite(ctx, tx, r, modCtx, input.Model, md, plan.fields, updated); hostErr != nil {
		return ORMMutateOutput{}, hostErr
	}
	if hostErr := runConstraintHook(ctx, r, modCtx, input.Model, "write", updated); hostErr != nil {
		return ORMMutateOutput{}, hostErr
	}
	if hostErr := writeAuditLogEntry(ctx, tx, modCtx, input.Model, md, "UPDATE", oldData, updated); hostErr != nil {
		return ORMMutateOutput{}, hostErr
	}
	if hostErr := writeChangeActivity(ctx, tx, modCtx, input.Model, md, oldData, updated); hostErr != nil {
		return ORMMutateOutput{}, hostErr
	}
	if err := emitRecordUpdatedEvent(ctx, insertClient, tx, modCtx, input.Model, updated, plan.fields); err != nil {
		return ORMMutateOutput{}, ormSQLErrorRetryable(err)
	}

	if err := commit(); err != nil {
		return ORMMutateOutput{}, &abi.HostError{Code: abi.ErrCodeCommitFailed, Message: err.Error()}
	}

	applyFieldMasking(modCtx, input.Model, []map[string]any{updated})
	return ORMMutateOutput{Record: updated}, nil
}

type mutationPlan struct {
	cols   []string
	deltas []any
	fields []string
}

// planMutation validates each op against the model and the caller's field
// security. An OnDeniedWrite(Ignore) field is dropped, as host.orm.write
// drops it.
func planMutation(modCtx *ModuleContext, qualifiedModel string, md model.ModelDeclaration, ops []abiv1.ORMMutateOp) (mutationPlan, *abi.HostError) {
	if len(ops) == 0 {
		return mutationPlan{}, &abi.HostError{Code: abi.ErrCodeValidationFailed, Message: "mutate requires at least one op"}
	}

	defs := make(map[string]model.FieldDef, len(md.Fields))
	for _, f := range md.Fields {
		defs[f.Name] = f.Def
	}

	var plan mutationPlan
	seen := make(map[string]bool, len(ops))
	for _, op := range ops {
		fieldDetails := map[string]any{"field": op.Field}
		def, known := defs[op.Field]
		switch {
		case !known:
			return mutationPlan{}, &abi.HostError{Code: abi.ErrCodeValidationFailed, Message: "unknown field " + op.Field, Details: fieldDetails}
		case seen[op.Field]:
			return mutationPlan{}, &abi.HostError{Code: abi.ErrCodeValidationFailed, Message: "field " + op.Field + " appears in more than one op", Details: fieldDetails}
		case def.IsPrimaryKey:
			return mutationPlan{}, &abi.HostError{Code: abi.ErrCodeValidationFailed, Message: "field " + op.Field + " is the primary key and cannot be mutated", Details: fieldDetails}
		case def.IsComputed:
			return mutationPlan{}, &abi.HostError{Code: abi.ErrCodeValidationFailed, Message: "field " + op.Field + " is computed and cannot be mutated", Details: fieldDetails}
		case !isNumericKind(def.Kind):
			return mutationPlan{}, &abi.HostError{Code: abi.ErrCodeValidationFailed, Message: "field " + op.Field + " is not numeric", Details: fieldDetails}
		case def.IsReadonly:
			return mutationPlan{}, &abi.HostError{Code: abi.ErrCodeFieldNotWritable, Message: "field " + op.Field + " is readonly and cannot be mutated", Details: fieldDetails}
		}
		seen[op.Field] = true

		if rule, denied := writeDeniedBy(modCtx, qualifiedModel, op.Field); denied {
			if rule.OnDeniedWrite == fieldsec.Ignore {
				continue
			}
			return mutationPlan{}, &abi.HostError{Code: abi.ErrCodeFieldWriteDenied, Message: "field " + op.Field + " requires permission " + rule.WritePermission, Details: fieldDetails}
		}

		delta, err := normalizeDelta(def.Kind, op.Delta)
		if err != nil {
			return mutationPlan{}, &abi.HostError{Code: abi.ErrCodeValidationFailed, Message: "field " + op.Field + ": " + err.Error(), Details: fieldDetails}
		}
		plan.cols = append(plan.cols, quoteIdentORM(op.Field))
		plan.deltas = append(plan.deltas, delta)
		plan.fields = append(plan.fields, op.Field)
	}

	if len(plan.cols) == 0 {
		return mutationPlan{}, &abi.HostError{Code: abi.ErrCodeValidationFailed, Message: "mutate has no fields to update"}
	}
	slices.Sort(plan.fields)
	return plan, nil
}

func isNumericKind(k model.FieldKind) bool {
	switch k {
	case model.KindInteger, model.KindBigInt, model.KindFloat, model.KindDecimal:
		return true
	}
	return false
}

// normalizeDelta converts a msgpack-decoded number to int64 for
// Integer/BigInt columns (rejecting fractions and int32 overflow) or
// float64 for Float/Decimal.
func normalizeDelta(kind model.FieldKind, v any) (any, error) {
	var i int64
	var f float64
	isInt := true
	switch n := v.(type) {
	case int:
		i = int64(n)
	case int8:
		i = int64(n)
	case int16:
		i = int64(n)
	case int32:
		i = int64(n)
	case int64:
		i = n
	case uint8:
		i = int64(n)
	case uint16:
		i = int64(n)
	case uint32:
		i = int64(n)
	case uint64:
		if n > math.MaxInt64 {
			return nil, errors.New("delta is out of range")
		}
		i = int64(n)
	case float32:
		f, isInt = float64(n), false
	case float64:
		f, isInt = n, false
	default:
		return nil, fmt.Errorf("delta must be a number, got %T", v)
	}

	if !isInt && (math.IsNaN(f) || math.IsInf(f, 0)) {
		return nil, errors.New("delta must be finite")
	}
	switch kind {
	case model.KindInteger, model.KindBigInt:
		if !isInt {
			if f != math.Trunc(f) || f < math.MinInt64 || f >= math.MaxInt64 {
				return nil, errors.New("an integer field needs a whole-number delta")
			}
			i = int64(f)
		}
		if kind == model.KindInteger && (i < math.MinInt32 || i > math.MaxInt32) {
			return nil, errors.New("delta is out of range for an integer field")
		}
		return i, nil
	default:
		if isInt {
			return float64(i), nil
		}
		return f, nil
	}
}

// lockRowByPK reads the row under a row lock; a row the caller cannot
// see yields nil, and the UPDATE that follows reports not-found.
func lockRowByPK(ctx context.Context, tx *sql.Tx, md model.ModelDeclaration, pkCol, id string) (map[string]any, *abi.HostError) {
	row, hostErr := selectRowByPK(ctx, tx, md, pkCol, id, "FOR UPDATE")
	if hostErr != nil && hostErr.Code == abi.ErrCodeNotFound {
		return nil, nil
	}
	return row, hostErr
}

// diagnoseZeroRowMutation separates a missing or RLS-hidden row
// (orm.not_found, as diagnoseZeroRowWrite) from a false guard.
func diagnoseZeroRowMutation(ctx context.Context, tx *sql.Tx, table, pkColQuoted, id string) *abi.HostError {
	var exists bool
	if err := tx.QueryRowContext(ctx, fmt.Sprintf("SELECT EXISTS (SELECT 1 FROM %s WHERE %s = $1)", table, pkColQuoted), id).Scan(&exists); err != nil {
		return &abi.HostError{Code: abi.ErrCodeUnavailable, Message: err.Error()}
	}
	if !exists {
		return &abi.HostError{Code: abi.ErrCodeNotFound, Message: "record not found"}
	}
	return &abi.HostError{Code: abi.ErrCodePreconditionFailed, Message: "the guard did not hold for the record's current state"}
}

// translateMutateError maps overflow and CHECK violations to
// orm.validation_failed and defers the rest to translateWriteError.
func translateMutateError(err error, md model.ModelDeclaration) *abi.HostError {
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok {
		switch pgErr.Code {
		case "22003", "23514": // numeric_value_out_of_range, check_violation
			return &abi.HostError{Code: abi.ErrCodeValidationFailed, Message: pgErr.Message, Details: map[string]any{"constraint": pgErr.ConstraintName}}
		}
	}
	return translateWriteError(err, md)
}
