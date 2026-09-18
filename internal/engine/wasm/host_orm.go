package wasm

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/djangbahevans/goerp/internal/engine/cache"
	"github.com/djangbahevans/goerp/internal/engine/domain"
	"github.com/djangbahevans/goerp/internal/engine/fieldsec"
	"github.com/djangbahevans/goerp/internal/engine/permission"
	"github.com/djangbahevans/goerp/sdk/go/model"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/vmihailenco/msgpack/v5"
)

// registerHostORM attaches host.orm's read half (search/search_read/read,
// this file), write half (create/write/unlink, host_orm_write.go), and
// Transient-model routing (host_orm_transient.go) to the runtime. Lives
// in the wasm package for the same import-cycle reason registerHostDB
// does (host_db.go) — its closures need direct access to *sql.DB and the
// Runtime's instance registry. insertClient is the same never-Start()'d
// river.Client[*sql.Tx] registerHostEvent uses, threaded through so the
// write half can emit orm.record.* events transactionally
// (host_orm_write.go's emitRecordEvent) without a second client.
// cacheClient backs Transient-model create/read/write/unlink
// (host_orm_transient.go) — Table-backed models never touch it.
//
// dispatchORMRoute (goerp#346, EnableOps' HTTP entry point) is a separate
// ticket — nothing here derives or serves an HTTP route.
func registerHostORM(ctx context.Context, rt wazero.Runtime, r *Runtime, db *sql.DB, insertClient *river.Client[*sql.Tx], cacheClient *cache.Client) error {
	_, err := rt.NewHostModuleBuilder("host.orm").
		NewFunctionBuilder().WithFunc(makeORMSearch(r, db)).Export("search").
		NewFunctionBuilder().WithFunc(makeORMSearchRead(r, db)).Export("search_read").
		NewFunctionBuilder().WithFunc(makeORMRead(r, db, cacheClient)).Export("read").
		NewFunctionBuilder().WithFunc(makeORMCreate(r, db, insertClient, cacheClient)).Export("create").
		NewFunctionBuilder().WithFunc(makeORMCreateBatch(r, db, insertClient)).Export("create_batch").
		NewFunctionBuilder().WithFunc(makeORMFirstOrCreate(r, db, insertClient)).Export("first_or_create").
		NewFunctionBuilder().WithFunc(makeORMWrite(r, db, insertClient, cacheClient)).Export("write").
		NewFunctionBuilder().WithFunc(makeORMWriteMany(r, db, insertClient)).Export("write_many").
		NewFunctionBuilder().WithFunc(makeORMWriteWhere(r, db, insertClient)).Export("write_where").
		NewFunctionBuilder().WithFunc(makeORMUnlink(r, db, insertClient, cacheClient)).Export("unlink").
		Instantiate(ctx)
	return err
}

type ORMSearchInput struct {
	Model  string `msgpack:"model"`
	Domain string `msgpack:"domain"`
	Order  string `msgpack:"order,omitempty"`
	Limit  int    `msgpack:"limit,omitempty"`
	Offset int    `msgpack:"offset,omitempty"`
	TxID   string `msgpack:"tx_id"`
}

type ORMSearchOutput struct {
	IDs   []string `msgpack:"ids"`
	Count int64    `msgpack:"count"`
}

type ORMSearchReadInput struct {
	Model  string   `msgpack:"model"`
	Domain string   `msgpack:"domain"`
	Fields []string `msgpack:"fields,omitempty"`
	Order  string   `msgpack:"order,omitempty"`
	Limit  int      `msgpack:"limit,omitempty"`
	Offset int      `msgpack:"offset,omitempty"`
	Cursor string   `msgpack:"cursor,omitempty"`
	TxID   string   `msgpack:"tx_id"`
}

type ORMSearchReadOutput struct {
	Records    []map[string]any `msgpack:"records"`
	NextCursor string           `msgpack:"next_cursor,omitempty"`
}

type ORMReadInput struct {
	Model  string   `msgpack:"model"`
	IDs    []string `msgpack:"ids"`
	Fields []string `msgpack:"fields,omitempty"`
	TxID   string   `msgpack:"tx_id"`
}

type ORMReadOutput struct {
	Records []map[string]any `msgpack:"records"`
}

func makeORMSearch(r *Runtime, db *sql.DB) func(ctx context.Context, m api.Module, ptr, length uint32) uint64 {
	return func(ctx context.Context, m api.Module, ptr, length uint32) uint64 {
		inst := r.InstanceForModule(m)
		modCtx := inst.ModuleContext()
		allocate := inst.allocate

		inputBytes, err := abi.ReadFromModule(m.Memory(), ptr, length)
		if err != nil {
			return abi.EncodeHostError(ctx, m, allocate, abi.MemoryFault())
		}
		var input ORMSearchInput
		if err := msgpack.Unmarshal(inputBytes, &input); err != nil {
			return abi.EncodeHostError(ctx, m, allocate, abi.DeserializeError(err))
		}

		out, hostErr := ORMSearch(ctx, db, modCtx, input)
		if hostErr != nil {
			return abi.EncodeHostError(ctx, m, allocate, hostErr)
		}
		return abi.WriteToModule(ctx, m, allocate, out)
	}
}

// ORMSearch is host.orm search's plain-Go core, callable without a WASM
// instance in the loop — the shared entry point for both the WASM
// closure above and dispatchORMRoute (goerp#346, HTTP-served EnableOps
// routes). Enforces db.read the same way regardless of caller, since
// modCtx.Capabilities() reflects the calling module's own declared
// capabilities, not the transport that reached it.
func ORMSearch(ctx context.Context, db *sql.DB, modCtx *ModuleContext, input ORMSearchInput) (ORMSearchOutput, *abi.HostError) {
	if !modCtx.Capabilities().Has(abi.CapDBRead) {
		return ORMSearchOutput{}, abi.CapabilityDenied("db.read")
	}

	md, ok := resolveModel(modCtx, input.Model)
	if !ok {
		return ORMSearchOutput{}, &abi.HostError{Code: abi.ErrCodeModelNotFound, Message: "model " + input.Model + " is not declared by this module"}
	}
	if md.Backend == model.BackendTransient {
		return ORMSearchOutput{}, &abi.HostError{Code: abi.ErrCodeTransientNotListable, Message: "model " + input.Model + " is Transient — there is no table to search"}
	}

	whereFrag, args, hostErr := compileDomain(input.Domain)
	if hostErr != nil {
		return ORMSearchOutput{}, hostErr
	}

	pkCol, ok := primaryKeyColumn(md)
	if !ok {
		return ORMSearchOutput{}, &abi.HostError{Code: abi.ErrCodeModelNotFound, Message: "model " + input.Model + " declares no primary key field"}
	}

	tx, finish, hostErr := resolveORMReadTx(ctx, db, modCtx, input.TxID)
	if hostErr != nil {
		return ORMSearchOutput{}, hostErr
	}
	defer finish()

	table := quoteIdentORM(tableNameForORM(md))
	pkColQuoted := quoteIdentORM(pkCol)

	var count int64
	countSQL := fmt.Sprintf("SELECT count(*) FROM %s WHERE %s", table, whereFrag)
	if err := tx.QueryRowContext(ctx, countSQL, args...).Scan(&count); err != nil {
		return ORMSearchOutput{}, &abi.HostError{Code: abi.ErrCodeUnavailable, Message: err.Error()}
	}

	listSQL := fmt.Sprintf("SELECT %s FROM %s WHERE %s%s", pkColQuoted, table, whereFrag, orderLimitOffsetClause(input.Order, input.Limit, input.Offset))
	rows, err := tx.QueryContext(ctx, listSQL, args...)
	if err != nil {
		return ORMSearchOutput{}, &abi.HostError{Code: abi.ErrCodeUnavailable, Message: err.Error()}
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return ORMSearchOutput{}, &abi.HostError{Code: abi.ErrCodeUnavailable, Message: err.Error()}
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return ORMSearchOutput{}, &abi.HostError{Code: abi.ErrCodeUnavailable, Message: err.Error()}
	}

	return ORMSearchOutput{IDs: ids, Count: count}, nil
}

func makeORMSearchRead(r *Runtime, db *sql.DB) func(ctx context.Context, m api.Module, ptr, length uint32) uint64 {
	return func(ctx context.Context, m api.Module, ptr, length uint32) uint64 {
		inst := r.InstanceForModule(m)
		modCtx := inst.ModuleContext()
		allocate := inst.allocate

		inputBytes, err := abi.ReadFromModule(m.Memory(), ptr, length)
		if err != nil {
			return abi.EncodeHostError(ctx, m, allocate, abi.MemoryFault())
		}
		var input ORMSearchReadInput
		if err := msgpack.Unmarshal(inputBytes, &input); err != nil {
			return abi.EncodeHostError(ctx, m, allocate, abi.DeserializeError(err))
		}

		out, hostErr := ORMSearchRead(ctx, db, modCtx, input)
		if hostErr != nil {
			return abi.EncodeHostError(ctx, m, allocate, hostErr)
		}
		return abi.WriteToModule(ctx, m, allocate, out)
	}
}

// ORMSearchRead is host.orm search_read's plain-Go core — see ORMSearch's
// doc comment for the shared-entry-point rationale.
func ORMSearchRead(ctx context.Context, db *sql.DB, modCtx *ModuleContext, input ORMSearchReadInput) (ORMSearchReadOutput, *abi.HostError) {
	if !modCtx.Capabilities().Has(abi.CapDBRead) {
		return ORMSearchReadOutput{}, abi.CapabilityDenied("db.read")
	}

	md, ok := resolveModel(modCtx, input.Model)
	if !ok {
		return ORMSearchReadOutput{}, &abi.HostError{Code: abi.ErrCodeModelNotFound, Message: "model " + input.Model + " is not declared by this module"}
	}
	if md.Backend == model.BackendTransient {
		return ORMSearchReadOutput{}, &abi.HostError{Code: abi.ErrCodeTransientNotListable, Message: "model " + input.Model + " is Transient — there is no table to search"}
	}

	pkCol, ok := primaryKeyColumn(md)
	if !ok {
		return ORMSearchReadOutput{}, &abi.HostError{Code: abi.ErrCodeModelNotFound, Message: "model " + input.Model + " declares no primary key field"}
	}

	columns, hostErr := readableColumns(input.Model, md, input.Fields)
	if hostErr != nil {
		return ORMSearchReadOutput{}, hostErr
	}

	whereFrag, args, hostErr := compileDomain(input.Domain)
	if hostErr != nil {
		return ORMSearchReadOutput{}, hostErr
	}

	if input.Cursor != "" {
		args = append(args, input.Cursor)
		whereFrag = fmt.Sprintf("(%s) AND (%s > $%d)", whereFrag, quoteIdentORM(pkCol), len(args))
	}

	tx, finish, hostErr := resolveORMReadTx(ctx, db, modCtx, input.TxID)
	if hostErr != nil {
		return ORMSearchReadOutput{}, hostErr
	}
	defer finish()

	// Cursor pagination always computes NextCursor from pkCol below — if
	// the caller's own Fields projection excluded it, select it anyway
	// (and strip it back out of each record afterward) rather than
	// silently losing the ability to page past a full page.
	pkRequested := slices.Contains(columns, pkCol)
	queryColumns := columns
	if !pkRequested {
		queryColumns = slices.Concat(columns, []string{pkCol})
	}

	table := quoteIdentORM(tableNameForORM(md))
	selectCols := make([]string, len(queryColumns))
	for i, c := range queryColumns {
		selectCols[i] = quoteIdentORM(c)
	}

	order := input.Order
	limit := input.Limit
	if input.Cursor != "" || order == "" {
		// Cursor-based pagination always walks the primary key in
		// ascending order — combining an arbitrary caller-supplied
		// ORDER BY with keyset pagination would need a composite
		// cursor, which is out of scope here.
		order = pkCol
	}

	sqlStr := fmt.Sprintf("SELECT %s FROM %s WHERE %s%s",
		strings.Join(selectCols, ", "), table, whereFrag, orderLimitOffsetClause(order, limit, input.Offset))
	rows, err := tx.QueryContext(ctx, sqlStr, args...)
	if err != nil {
		return ORMSearchReadOutput{}, &abi.HostError{Code: abi.ErrCodeUnavailable, Message: err.Error()}
	}
	defer rows.Close()

	records, err := scanRowsToMaps(rows)
	if err != nil {
		return ORMSearchReadOutput{}, &abi.HostError{Code: abi.ErrCodeUnavailable, Message: err.Error()}
	}

	applyFieldMasking(modCtx, input.Model, records)

	if err := expandRelations(ctx, tx, modCtx, md, columns, records); err != nil {
		return ORMSearchReadOutput{}, &abi.HostError{Code: abi.ErrCodeUnavailable, Message: err.Error()}
	}

	// A page has a next one only if it came back full — whether or not
	// this call itself was already paging via input.Cursor. A short last
	// page on a cursor-based request must not claim there's more.
	var nextCursor string
	if limit > 0 && len(records) == limit {
		if last, ok := records[len(records)-1][pkCol]; ok {
			nextCursor = fmt.Sprintf("%v", last)
		}
	}

	// pkCol was only added to the query above to make the cursor
	// computable — strip it back out so a caller who didn't request it
	// doesn't see it in the response.
	if !pkRequested {
		for _, record := range records {
			delete(record, pkCol)
		}
	}

	return ORMSearchReadOutput{Records: records, NextCursor: nextCursor}, nil
}

// aggregateSQLFuncs maps a PivotValue.aggregation name (manifest-spec.md
// §9.5) to the SQL aggregate function it compiles to. "count_distinct"
// isn't here — it needs a DISTINCT keyword inside the call, not just a
// different function name, so ORMAggregate special-cases it.
var aggregateSQLFuncs = map[string]string{
	"sum":   "SUM",
	"count": "COUNT",
	"avg":   "AVG",
	"min":   "MIN",
	"max":   "MAX",
}

type ORMAggregateValue struct {
	Field       string
	Aggregation string
}

type ORMAggregateInput struct {
	Model   string
	Domain  string
	Rows    []string
	Columns []string
	Values  []ORMAggregateValue
}

type ORMAggregateOutput struct {
	// Cells mirror the wire shape verbatim (view-system.md §8) — each is
	// {"row": []any, "column": []any, "values": map[string]any}, with a
	// nil entry marking a GROUP BY ROLLUP subtotal above that axis
	// position rather than a real grouped value.
	Cells []map[string]any
}

// ORMAggregate is the engine-native counterpart to ORMSearchRead for a
// Pivot-enabled model's GET {resource}/pivot route (dispatchORMPivot) —
// there is no WASM-guest-callable host.orm import for this, unlike every
// other function in this file: a module has no way to run a real
// Postgres-side GROUP BY today (sdk/go/orm only exposes row-shaped
// Search/SearchRead/Read), so aggregation is engine-native only, gated
// behind EnableOps(model.Pivot) the same way List/Get already are.
//
// One query produces every row/column subtotal a collapsed PivotGrid
// group needs: GROUP BY ROLLUP(rows...), ROLLUP(columns...) generates the
// finest-grain groups plus every coarser rollup (including the grand
// total) in a single pass, and GROUPING() on each dimension column
// disambiguates "rolled up" from "genuinely NULL in the data".
func ORMAggregate(ctx context.Context, db *sql.DB, modCtx *ModuleContext, input ORMAggregateInput) (ORMAggregateOutput, *abi.HostError) {
	if !modCtx.Capabilities().Has(abi.CapDBRead) {
		return ORMAggregateOutput{}, abi.CapabilityDenied("db.read")
	}

	md, ok := resolveModel(modCtx, input.Model)
	if !ok {
		return ORMAggregateOutput{}, &abi.HostError{Code: abi.ErrCodeModelNotFound, Message: "model " + input.Model + " is not declared by this module"}
	}
	if md.Backend == model.BackendTransient {
		return ORMAggregateOutput{}, &abi.HostError{Code: abi.ErrCodeTransientNotListable, Message: "model " + input.Model + " is Transient — there is no table to aggregate"}
	}

	if len(input.Rows) == 0 && len(input.Columns) == 0 {
		return ORMAggregateOutput{}, &abi.HostError{Code: abi.ErrCodeValidationFailed, Message: "pivot requires at least one rows or columns field"}
	}
	if len(input.Values) == 0 {
		return ORMAggregateOutput{}, &abi.HostError{Code: abi.ErrCodeValidationFailed, Message: "pivot requires at least one values entry"}
	}

	declared := make(map[string]model.FieldDef, len(md.Fields))
	for _, f := range md.Fields {
		declared[f.Name] = f.Def
	}
	fieldSecReg := modCtx.FieldSecRegistry()
	permReg := modCtx.PermissionRegistry()

	checkField := func(name string) *abi.HostError {
		def, ok := declared[name]
		if !ok {
			return &abi.HostError{Code: abi.ErrCodeFieldUnknown, Message: "field " + name + " is not declared on " + input.Model, Details: map[string]any{"field": name}}
		}
		if def.Kind == model.KindOne2Many {
			return &abi.HostError{Code: abi.ErrCodeFieldUnknown, Message: "field " + name + " is a One2Many relation and cannot be aggregated", Details: map[string]any{"field": name}}
		}
		if fieldSecReg == nil {
			return nil
		}
		if rule, ok := fieldSecReg.Rule(input.Model, name); ok && rule.ReadPermission != "" && !callerHasPermission(modCtx, permReg, rule.ReadPermission) {
			return &abi.HostError{Code: abi.ErrCodeFieldReadDenied, Message: "field " + name + " requires permission " + rule.ReadPermission, Details: map[string]any{"field": name}}
		}
		return nil
	}

	dimFields := make([]string, 0, len(input.Rows)+len(input.Columns))
	dimFields = append(dimFields, input.Rows...)
	dimFields = append(dimFields, input.Columns...)
	for _, f := range dimFields {
		if hostErr := checkField(f); hostErr != nil {
			return ORMAggregateOutput{}, hostErr
		}
	}
	for _, v := range input.Values {
		if hostErr := checkField(v.Field); hostErr != nil {
			return ORMAggregateOutput{}, hostErr
		}
		if v.Aggregation != "count_distinct" {
			if _, ok := aggregateSQLFuncs[v.Aggregation]; !ok {
				return ORMAggregateOutput{}, &abi.HostError{Code: abi.ErrCodeValidationFailed, Message: "unknown aggregation " + v.Aggregation, Details: map[string]any{"aggregation": v.Aggregation}}
			}
		}
	}

	whereFrag, args, hostErr := compileDomain(input.Domain)
	if hostErr != nil {
		return ORMAggregateOutput{}, hostErr
	}

	tx, finish, hostErr := resolveORMReadTx(ctx, db, modCtx, "")
	if hostErr != nil {
		return ORMAggregateOutput{}, hostErr
	}
	defer finish()

	table := quoteIdentORM(tableNameForORM(md))

	selectExprs := make([]string, 0, len(dimFields)+len(input.Values)+len(dimFields))
	for _, f := range dimFields {
		selectExprs = append(selectExprs, quoteIdentORM(f))
	}
	valueAliases := make([]string, len(input.Values))
	for i, v := range input.Values {
		alias := v.Field + "_" + v.Aggregation
		valueAliases[i] = alias
		var expr string
		if v.Aggregation == "count_distinct" {
			expr = fmt.Sprintf("COUNT(DISTINCT %s)", quoteIdentORM(v.Field))
		} else {
			expr = fmt.Sprintf("%s(%s)", aggregateSQLFuncs[v.Aggregation], quoteIdentORM(v.Field))
		}
		selectExprs = append(selectExprs, fmt.Sprintf("%s AS %s", expr, quoteIdentORM(alias)))
	}
	groupingAliases := make(map[string]string, len(dimFields))
	for _, f := range dimFields {
		groupingAlias := "__grouping_" + f
		groupingAliases[f] = groupingAlias
		selectExprs = append(selectExprs, fmt.Sprintf("(GROUPING(%s) = 1) AS %s", quoteIdentORM(f), quoteIdentORM(groupingAlias)))
	}

	var groupByClause string
	switch {
	case len(input.Rows) > 0 && len(input.Columns) > 0:
		groupByClause = fmt.Sprintf("GROUP BY ROLLUP(%s), ROLLUP(%s)", quoteIdentListORM(input.Rows), quoteIdentListORM(input.Columns))
	case len(input.Rows) > 0:
		groupByClause = fmt.Sprintf("GROUP BY ROLLUP(%s)", quoteIdentListORM(input.Rows))
	default:
		groupByClause = fmt.Sprintf("GROUP BY ROLLUP(%s)", quoteIdentListORM(input.Columns))
	}

	sqlStr := fmt.Sprintf("SELECT %s FROM %s WHERE %s %s",
		strings.Join(selectExprs, ", "), table, whereFrag, groupByClause)
	sqlRows, err := tx.QueryContext(ctx, sqlStr, args...)
	if err != nil {
		return ORMAggregateOutput{}, &abi.HostError{Code: abi.ErrCodeUnavailable, Message: err.Error()}
	}
	defer sqlRows.Close()

	records, err := scanRowsToMaps(sqlRows)
	if err != nil {
		return ORMAggregateOutput{}, &abi.HostError{Code: abi.ErrCodeUnavailable, Message: err.Error()}
	}

	cells := make([]map[string]any, 0, len(records))
	for _, rec := range records {
		rowVals := dimensionValues(rec, input.Rows, groupingAliases)
		colVals := dimensionValues(rec, input.Columns, groupingAliases)
		valuesOut := make(map[string]any, len(valueAliases))
		for _, alias := range valueAliases {
			valuesOut[alias] = rec[alias]
		}
		cells = append(cells, map[string]any{"row": rowVals, "column": colVals, "values": valuesOut})
	}

	return ORMAggregateOutput{Cells: cells}, nil
}

// dimensionValues reads fields' grouped values out of one aggregation
// result row, replacing a ROLLUP-collapsed position with nil per its
// GROUPING() flag rather than the SQL NULL GROUP BY ROLLUP itself emits
// there (which would be indistinguishable from a genuinely NULL value).
func dimensionValues(rec map[string]any, fields []string, groupingAliases map[string]string) []any {
	vals := make([]any, len(fields))
	for i, f := range fields {
		if rolledUp, _ := rec[groupingAliases[f]].(bool); rolledUp {
			vals[i] = nil
		} else {
			vals[i] = rec[f]
		}
	}
	return vals
}

func quoteIdentListORM(names []string) string {
	quoted := make([]string, len(names))
	for i, n := range names {
		quoted[i] = quoteIdentORM(n)
	}
	return strings.Join(quoted, ", ")
}

func makeORMRead(r *Runtime, db *sql.DB, cacheClient *cache.Client) func(ctx context.Context, m api.Module, ptr, length uint32) uint64 {
	return func(ctx context.Context, m api.Module, ptr, length uint32) uint64 {
		inst := r.InstanceForModule(m)
		modCtx := inst.ModuleContext()
		allocate := inst.allocate

		inputBytes, err := abi.ReadFromModule(m.Memory(), ptr, length)
		if err != nil {
			return abi.EncodeHostError(ctx, m, allocate, abi.MemoryFault())
		}
		var input ORMReadInput
		if err := msgpack.Unmarshal(inputBytes, &input); err != nil {
			return abi.EncodeHostError(ctx, m, allocate, abi.DeserializeError(err))
		}

		out, hostErr := ORMRead(ctx, db, cacheClient, modCtx, input)
		if hostErr != nil {
			return abi.EncodeHostError(ctx, m, allocate, hostErr)
		}
		return abi.WriteToModule(ctx, m, allocate, out)
	}
}

// ORMRead is host.orm read's plain-Go core — see ORMSearch's doc comment
// for the shared-entry-point rationale. Branches to transientRead
// (host_orm_transient.go) for Transient-backed models internally, so
// callers never need to know a model's backend before calling in.
func ORMRead(ctx context.Context, db *sql.DB, cacheClient *cache.Client, modCtx *ModuleContext, input ORMReadInput) (ORMReadOutput, *abi.HostError) {
	if !modCtx.Capabilities().Has(abi.CapDBRead) {
		return ORMReadOutput{}, abi.CapabilityDenied("db.read")
	}

	md, ok := resolveModel(modCtx, input.Model)
	if !ok {
		return ORMReadOutput{}, &abi.HostError{Code: abi.ErrCodeModelNotFound, Message: "model " + input.Model + " is not declared by this module"}
	}

	if md.Backend == model.BackendTransient {
		return transientRead(ctx, cacheClient, modCtx, input.Model, input.IDs)
	}

	pkCol, ok := primaryKeyColumn(md)
	if !ok {
		return ORMReadOutput{}, &abi.HostError{Code: abi.ErrCodeModelNotFound, Message: "model " + input.Model + " declares no primary key field"}
	}

	columns, hostErr := readableColumns(input.Model, md, input.Fields)
	if hostErr != nil {
		return ORMReadOutput{}, hostErr
	}

	if len(input.IDs) == 0 {
		return ORMReadOutput{Records: []map[string]any{}}, nil
	}

	tx, finish, hostErr := resolveORMReadTx(ctx, db, modCtx, input.TxID)
	if hostErr != nil {
		return ORMReadOutput{}, hostErr
	}
	defer finish()

	table := quoteIdentORM(tableNameForORM(md))
	selectCols := make([]string, len(columns))
	for i, c := range columns {
		selectCols[i] = quoteIdentORM(c)
	}

	placeholders := make([]string, len(input.IDs))
	args := make([]any, len(input.IDs))
	for i, id := range input.IDs {
		placeholders[i] = fmt.Sprintf("$%d", i+1)
		args[i] = id
	}

	sqlStr := fmt.Sprintf("SELECT %s FROM %s WHERE %s IN (%s)",
		strings.Join(selectCols, ", "), table, quoteIdentORM(pkCol), strings.Join(placeholders, ", "))
	rows, err := tx.QueryContext(ctx, sqlStr, args...)
	if err != nil {
		return ORMReadOutput{}, &abi.HostError{Code: abi.ErrCodeUnavailable, Message: err.Error()}
	}
	defer rows.Close()

	records, err := scanRowsToMaps(rows)
	if err != nil {
		return ORMReadOutput{}, &abi.HostError{Code: abi.ErrCodeUnavailable, Message: err.Error()}
	}

	applyFieldMasking(modCtx, input.Model, records)

	if err := expandRelations(ctx, tx, modCtx, md, columns, records); err != nil {
		return ORMReadOutput{}, &abi.HostError{Code: abi.ErrCodeUnavailable, Message: err.Error()}
	}

	return ORMReadOutput{Records: records}, nil
}

// resolveModel resolves an ABI-level "{module}.{resource}" model name
// against the calling module's own declared models — a module can only
// address its own models through host.orm, never another module's.
func resolveModel(modCtx *ModuleContext, qualifiedName string) (model.ModelDeclaration, bool) {
	prefix := modCtx.ModuleName + "."
	if !strings.HasPrefix(qualifiedName, prefix) {
		return model.ModelDeclaration{}, false
	}
	bareName := strings.TrimPrefix(qualifiedName, prefix)
	for _, md := range modCtx.ModelDecls() {
		if md.Name == bareName {
			return md, true
		}
	}
	return model.ModelDeclaration{}, false
}

func primaryKeyColumn(md model.ModelDeclaration) (string, bool) {
	for _, f := range md.Fields {
		if f.Def.IsPrimaryKey {
			return f.Name, true
		}
	}
	return "", false
}

// tableNameForORM mirrors schema.TableNameFor's Table-or-snakeCase(Name)
// fallback exactly — duplicated locally rather than imported, since
// internal/engine/schema's own test suite imports this package (a real
// compiled-module integration test), and this package importing schema
// back would be a cycle. host_orm_test.go cross-checks this against
// schema.TableNameFor directly (safe in a test file, since only schema's
// test files import wasm, not schema's production code).
func tableNameForORM(md model.ModelDeclaration) string {
	if md.Table != "" {
		return md.Table
	}
	return snakeCaseORM(md.Name)
}

func snakeCaseORM(name string) string {
	var b strings.Builder
	prevLower := false
	for _, r := range name {
		switch {
		case r == '.':
			b.WriteByte('_')
			prevLower = false
		case unicode.IsUpper(r):
			if prevLower {
				b.WriteByte('_')
			}
			b.WriteRune(unicode.ToLower(r))
			prevLower = false
		default:
			b.WriteRune(r)
			prevLower = true
		}
	}
	return b.String()
}

// compileDomain parses and compiles a caller-supplied search domain to a
// parameterized SQL WHERE fragment, mapping a parse/compile failure to
// orm.domain_invalid (host-abi-reference.md §5a) rather than surfacing
// the raw parser error.
func compileDomain(src string) (string, []any, *abi.HostError) {
	if src == "" {
		return "true", nil, nil
	}
	expr, err := domain.Parse(src)
	if err != nil {
		return "", nil, &abi.HostError{Code: abi.ErrCodeDomainInvalid, Message: err.Error()}
	}
	frag, args, err := domain.CompileToSQL(expr)
	if err != nil {
		return "", nil, &abi.HostError{Code: abi.ErrCodeDomainInvalid, Message: err.Error()}
	}
	return frag, args, nil
}

// readableColumns validates the caller's requested field list against the
// model's declared fields (orm.field_unknown for anything not declared)
// and, when empty, defaults to every declared field. A One2Many field has
// no backing column (go-sdk-reference.md §22 "One2Many" — its data lives
// on the child model's own Many2One column, served via a separate
// sub-resource route), so it's excluded from the default list and
// rejected if explicitly requested.
func readableColumns(qualifiedModel string, md model.ModelDeclaration, requested []string) ([]string, *abi.HostError) {
	declared := make(map[string]model.FieldDef, len(md.Fields))
	all := make([]string, 0, len(md.Fields))
	for _, f := range md.Fields {
		declared[f.Name] = f.Def
		if f.Def.Kind == model.KindOne2Many {
			continue
		}
		all = append(all, f.Name)
	}

	if len(requested) == 0 {
		return all, nil
	}
	for _, f := range requested {
		def, ok := declared[f]
		if !ok {
			return nil, &abi.HostError{Code: abi.ErrCodeFieldUnknown, Message: "field " + f + " is not declared on " + qualifiedModel}
		}
		if def.Kind == model.KindOne2Many {
			return nil, &abi.HostError{Code: abi.ErrCodeFieldUnknown, Message: "field " + f + " is a One2Many relation and cannot be selected directly"}
		}
	}
	return requested, nil
}

// applyFieldMasking applies each field's OnDeniedRead behavior in place,
// for every field with a declared read rule the caller's PermissionSet
// doesn't satisfy (manifest-spec.md §8a, auth-internals.md §12). Applies
// uniformly to every record passed in — an extension-module field
// (model.Extend()) gets the same enforcement as a base-model field,
// since the field list this function iterates comes from the record map
// itself, not from which module declared the field.
//
// Only checks the record's own top-level keys — it does not recurse into
// a Many2One's expanded {id, display_name} object (expandRelations,
// host_orm_relations.go) or an engine.Embeds-declared sub-record (not
// yet implemented). In practice this is a narrow gap: expandRelations
// only ever selects a target model's primary key and display_name, so a
// restricted field can't leak through it unless display_name itself
// carries an unusual .Access() rule — that specific case is unenforced
// today. Recursive enforcement for both is deferred until engine.Embeds
// exists and there's a real shape to test against.
func applyFieldMasking(modCtx *ModuleContext, qualifiedModel string, records []map[string]any) {
	reg := modCtx.FieldSecRegistry()
	if reg == nil {
		return
	}
	permReg := modCtx.PermissionRegistry()

	for _, record := range records {
		for fieldName, value := range record {
			rule, ok := reg.Rule(qualifiedModel, fieldName)
			if !ok || rule.ReadPermission == "" || callerHasPermission(modCtx, permReg, rule.ReadPermission) {
				continue
			}
			switch rule.OnDeniedRead {
			case fieldsec.Mask:
				record[fieldName] = applyMaskPattern(rule.MaskPattern, value)
			case fieldsec.Nullify:
				record[fieldName] = nil
			default: // fieldsec.Omit
				delete(record, fieldName)
			}
		}
	}
}

// callerHasPermission reports whether modCtx's caller's PermissionSet
// includes permissionName, resolved against permReg's stable bitfield
// index. A nil permReg (no registry in this request's snapshot) or an
// unregistered permission name both fail closed — deny by default,
// the same posture the "unevaluated check" placeholder this replaces
// already held.
func callerHasPermission(modCtx *ModuleContext, permReg *permission.PermissionRegistry, permissionName string) bool {
	if permReg == nil {
		return false
	}
	idx, ok := permReg.Index(permissionName)
	if !ok {
		return false
	}
	return modCtx.PermissionSet.Has(idx)
}

// applyMaskPattern substitutes pattern's {last4}/{first2}/{length}
// tokens against value's string form (manifest-spec.md §8a "On denied
// read behaviours"); literal characters elsewhere in pattern (e.g. the
// "****" in "****{last4}") pass through unchanged. A shorter-than-
// requested value substitutes its entire string rather than panicking
// on a slice out of range. A non-string value (numbers, bools, nested
// records) has no meaningful last4/first2 substring, so the pattern is
// returned as-is.
func applyMaskPattern(pattern string, value any) string {
	s, ok := value.(string)
	if !ok {
		return pattern
	}

	out := strings.ReplaceAll(pattern, "{length}", strconv.Itoa(len(s)))
	last4 := s
	if len(s) > 4 {
		last4 = s[len(s)-4:]
	}
	out = strings.ReplaceAll(out, "{last4}", last4)
	first2 := s
	if len(s) > 2 {
		first2 = s[:2]
	}
	out = strings.ReplaceAll(out, "{first2}", first2)
	return out
}

// resolveORMReadTx returns the transaction a host.orm read should run
// on: txID's borrowed transaction (registered by a prior host.db.begin,
// already tenant-scoped when makeDBBegin opened it) when txID is
// non-empty, or a freshly-opened tenant-scoped one otherwise. The
// returned finish func is a no-op for a borrowed transaction — committing
// or rolling it back is the caller's own host.db.commit/rollback
// responsibility, never host.orm's — and rolls back an owned one
// (read-only, so there's never anything to commit), matching every
// existing host.orm read function's own defer-rollback-only pattern.
func resolveORMReadTx(ctx context.Context, db *sql.DB, modCtx *ModuleContext, txID string) (tx *sql.Tx, finish func(), hostErr *abi.HostError) {
	if txID != "" {
		tx, ok := modCtx.Transaction(txID)
		if !ok {
			return nil, nil, &abi.HostError{Code: abi.ErrCodeTransactionNotFound, Message: "transaction ID does not exist or has expired"}
		}
		return tx, func() {}, nil
	}
	tx, err := beginTenantScopedRead(ctx, db, modCtx)
	if err != nil {
		return nil, nil, &abi.HostError{Code: abi.ErrCodeUnavailable, Message: err.Error(), Retry: true}
	}
	return tx, func() { _ = tx.Rollback() }, nil
}

// beginTenantScopedRead opens a read-only transaction with the same
// search_path/ABAC session-variable scoping host.db.begin uses
// (multitenancy-internals.md §5a "Layer 1"), so the RLS policies
// goerp#71/#72 install apply automatically — host.orm does nothing extra
// for row filtering, the table already enforces it.
func beginTenantScopedRead(ctx context.Context, db *sql.DB, modCtx *ModuleContext) (*sql.Tx, error) {
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	if err := applyTenantScope(ctx, tx, modCtx); err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	return tx, nil
}

func orderLimitOffsetClause(order string, limit, offset int) string {
	var b strings.Builder
	if order != "" {
		b.WriteString(" ORDER BY ")
		b.WriteString(orderByExpr(order))
	}
	if limit > 0 {
		fmt.Fprintf(&b, " LIMIT %d", limit)
	}
	if offset > 0 {
		fmt.Fprintf(&b, " OFFSET %d", offset)
	}
	return b.String()
}

// orderByExpr quotes an "order" option's field name, honoring a trailing
// " DESC" (host-abi-reference.md §5a: "field_name" or "field_name DESC").
func orderByExpr(order string) string {
	trimmed := strings.TrimSpace(order)
	if field, ok := strings.CutSuffix(trimmed, " DESC"); ok {
		return quoteIdentORM(strings.TrimSpace(field)) + " DESC"
	}
	field, _ := strings.CutSuffix(trimmed, " ASC")
	return quoteIdentORM(strings.TrimSpace(field))
}

func scanRowsToMaps(rows *sql.Rows) ([]map[string]any, error) {
	defer func() { _ = rows.Close() }()

	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}

	var records []map[string]any
	for rows.Next() {
		values, err := scanRowValues(rows, len(cols))
		if err != nil {
			return nil, err
		}
		record := make(map[string]any, len(cols))
		for i, col := range cols {
			record[col] = values[i]
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return records, nil
}

func quoteIdentORM(name string) string {
	return pgx.Identifier{name}.Sanitize()
}
