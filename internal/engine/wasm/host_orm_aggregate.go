package wasm

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/djangbahevans/goerp/internal/engine/modelmeta"
	"github.com/djangbahevans/goerp/internal/engine/modeltable"
	"github.com/djangbahevans/goerp/sdk/go/model"
	"github.com/tetratelabs/wazero/api"
	"github.com/vmihailenco/msgpack/v5"
)

func makeORMAggregate(r *Runtime, db *sql.DB) func(ctx context.Context, m api.Module, ptr, length uint32) uint64 {
	return func(ctx context.Context, m api.Module, ptr, length uint32) uint64 {
		inst := r.InstanceForModule(m)
		modCtx := inst.ModuleContext()
		allocate := inst.allocate

		inputBytes, err := abi.ReadFromModule(m.Memory(), ptr, length)
		if err != nil {
			return abi.EncodeHostError(ctx, m, allocate, abi.MemoryFault())
		}
		var input abiv1.ORMAggregateInput
		if err := msgpack.Unmarshal(inputBytes, &input); err != nil {
			return abi.EncodeHostError(ctx, m, allocate, abi.DeserializeError(err))
		}

		out, hostErr := ORMAggregate(ctx, db, modCtx, input)
		if hostErr != nil {
			return abi.EncodeHostError(ctx, m, allocate, hostErr)
		}
		return abi.WriteToModule(ctx, m, allocate, out)
	}
}

func aggregateValueAlias(v abiv1.ORMAggregateValue) string {
	return v.Field + "_" + v.Aggregation
}

func ORMAggregate(ctx context.Context, db *sql.DB, modCtx *ModuleContext, input abiv1.ORMAggregateInput) (abiv1.ORMAggregateOutput, *abiv1.HostError) {
	if !modCtx.Capabilities().Has(abi.CapDBRead) {
		return abiv1.ORMAggregateOutput{}, abi.CapabilityDenied("db.read")
	}

	md, ok := resolveReadableModel(modCtx, input.Model)
	if !ok {
		return abiv1.ORMAggregateOutput{}, &abiv1.HostError{Code: abiv1.ErrCodeModelNotFound, Message: "model " + input.Model + " is not declared by this module"}
	}
	if md.Backend == model.BackendTransient {
		return abiv1.ORMAggregateOutput{}, &abiv1.HostError{Code: abiv1.ErrCodeTransientNotListable, Message: "model " + input.Model + " is Transient — there is no table to aggregate"}
	}
	if len(input.Values) == 0 {
		return abiv1.ORMAggregateOutput{}, &abiv1.HostError{Code: abiv1.ErrCodeValidationFailed, Message: "aggregate requires at least one values entry"}
	}

	declared := make(map[string]model.FieldDef, len(md.Fields))
	for _, f := range md.Fields {
		declared[f.Name] = f.Def
	}
	fieldSecReg := modCtx.FieldSecRegistry()
	permReg := modCtx.PermissionRegistry()

	seenAliases := make(map[string]bool, len(input.Values))
	for _, v := range input.Values {
		switch v.Aggregation {
		case "count":
		case "count_distinct":
			if v.Field == "" {
				return abiv1.ORMAggregateOutput{}, &abiv1.HostError{Code: abiv1.ErrCodeValidationFailed, Message: "aggregation " + v.Aggregation + " requires a field", Details: map[string]any{"aggregation": v.Aggregation}}
			}
		default:
			if _, ok := aggregateSQLFuncs[v.Aggregation]; !ok {
				return abiv1.ORMAggregateOutput{}, &abiv1.HostError{Code: abiv1.ErrCodeValidationFailed, Message: "unknown aggregation " + v.Aggregation, Details: map[string]any{"aggregation": v.Aggregation}}
			}
			if v.Field == "" {
				return abiv1.ORMAggregateOutput{}, &abiv1.HostError{Code: abiv1.ErrCodeValidationFailed, Message: "aggregation " + v.Aggregation + " requires a field", Details: map[string]any{"aggregation": v.Aggregation}}
			}
		}

		if v.Field != "" {
			def, ok := declared[v.Field]
			if !ok {
				return abiv1.ORMAggregateOutput{}, &abiv1.HostError{Code: abiv1.ErrCodeFieldUnknown, Message: "field " + v.Field + " is not declared on " + input.Model, Details: map[string]any{"field": v.Field}}
			}
			if def.Kind == model.KindOne2Many || modelmeta.IsJoinRelation(def.Kind) {
				return abiv1.ORMAggregateOutput{}, &abiv1.HostError{Code: abiv1.ErrCodeFieldUnknown, Message: "field " + v.Field + " is a " + def.Kind.String() + " relation and cannot be aggregated", Details: map[string]any{"field": v.Field}}
			}
			if (v.Aggregation == "sum" || v.Aggregation == "avg") && !isNumericKind(def.Kind) {
				return abiv1.ORMAggregateOutput{}, &abiv1.HostError{Code: abiv1.ErrCodeValidationFailed, Message: "field " + v.Field + " is not numeric", Details: map[string]any{"field": v.Field}}
			}
			if (v.Aggregation == "min" || v.Aggregation == "max") && !isSortableKind(def.Kind) {
				return abiv1.ORMAggregateOutput{}, &abiv1.HostError{
					Code:    abiv1.ErrCodeValidationFailed,
					Message: "field " + v.Field + " is not sortable",
					Details: map[string]any{"field": v.Field},
				}
			}
			if fieldSecReg != nil {
				if rule, ok := fieldSecReg.Rule(input.Model, v.Field); ok && !fieldReadAllowed(modCtx, permReg, rule) {
					return abiv1.ORMAggregateOutput{}, &abiv1.HostError{Code: abiv1.ErrCodeFieldReadDenied, Message: "field " + v.Field + " requires permission " + rule.ReadPermission, Details: map[string]any{"field": v.Field}}
				}
			}
		}

		alias := aggregateValueAlias(v)
		if seenAliases[alias] {
			return abiv1.ORMAggregateOutput{}, &abiv1.HostError{Code: abiv1.ErrCodeValidationFailed, Message: "values entry " + alias + " appears more than once"}
		}
		seenAliases[alias] = true
	}

	whereFrag, args, hostErr := compileDomain(input.Domain, modCtx, md)
	if hostErr != nil {
		return abiv1.ORMAggregateOutput{}, hostErr
	}

	whereFrag = activeModelWhere(md, whereFrag)

	tx, finish, hostErr := resolveORMReadTx(ctx, db, modCtx, input.TxID)
	if hostErr != nil {
		return abiv1.ORMAggregateOutput{}, hostErr
	}
	defer finish()

	table := quoteIdentORM(modeltable.Name(md))

	selectExprs := make([]string, len(input.Values))
	for i, v := range input.Values {
		alias := aggregateValueAlias(v)

		var expr string
		switch {
		case v.Field == "":
			expr = "COUNT(*)"
		case v.Aggregation == "count":
			expr = fmt.Sprintf("COUNT(%s)", quoteIdentORM(v.Field))
		case v.Aggregation == "count_distinct":
			expr = fmt.Sprintf("COUNT(DISTINCT %s)", quoteIdentORM(v.Field))
		case v.Aggregation == "sum" || v.Aggregation == "avg":
			expr = fmt.Sprintf("COALESCE(%s(%s), 0)", aggregateSQLFuncs[v.Aggregation], quoteIdentORM(v.Field))
		default:
			expr = fmt.Sprintf("%s(%s)", aggregateSQLFuncs[v.Aggregation], quoteIdentORM(v.Field))
		}
		selectExprs[i] = fmt.Sprintf("%s AS %s", expr, quoteIdentORM(alias))
	}

	sqlStr := fmt.Sprintf("SELECT %s FROM %s WHERE %s", strings.Join(selectExprs, ", "), table, whereFrag)
	sqlRows, err := tx.QueryContext(ctx, sqlStr, args...)
	if err != nil {
		return abiv1.ORMAggregateOutput{}, ormSQLError(err)
	}
	defer sqlRows.Close()

	records, err := scanRowsToMaps(sqlRows)
	if err != nil {
		return abiv1.ORMAggregateOutput{}, ormSQLError(err)
	}

	return abiv1.ORMAggregateOutput{Values: records[0]}, nil
}

func isSortableKind(kind model.FieldKind) bool {
	if isNumericKind(kind) {
		return true
	}

	switch kind {
	case model.KindChar, model.KindText, model.KindSelection, model.KindEnum,
		model.KindDate, model.KindTimestampTZ:
		return true
	default:
		return false
	}
}
