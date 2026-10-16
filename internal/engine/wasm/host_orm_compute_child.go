package wasm

import (
	"context"
	"database/sql"
	"fmt"
	"maps"
	"reflect"
	"slices"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/computed"
)

// Parent fields are recomputed as a group so lifecycle events carry the complete
// stored result and one changed-field list, including both sides of a child move.
func recomputeChildParents(ctx context.Context, tx *sql.Tx, r *Runtime, modCtx *ModuleContext, deps []computed.Dependent, rows ...map[string]any) *abiv1.HostError {
	type parent struct {
		id   any
		deps []computed.Dependent
	}
	parents := map[string]*parent{}
	for _, dep := range deps {
		if dep.ViaChildFKField == "" {
			continue
		}
		for _, row := range rows {
			id := row[dep.ViaChildFKField]
			if id == nil {
				continue
			}
			key := dep.ModelDecl.QualifiedName(dep.ModuleName) + ":" + fmt.Sprint(id)
			group := parents[key]
			if group == nil {
				group = &parent{id: id}
				parents[key] = group
			}
			if !slices.ContainsFunc(group.deps, func(existing computed.Dependent) bool { return existing.Field == dep.Field }) {
				group.deps = append(group.deps, dep)
			}
		}
	}
	for _, key := range slices.Sorted(maps.Keys(parents)) {
		group := parents[key]
		dep := group.deps[0]
		pk, ok := primaryKeyColumn(dep.ModelDecl)
		if !ok {
			continue
		}
		before, hostErr := fetchStoredRowByPK(ctx, tx, dep.ModelDecl, pk, group.id)
		if hostErr != nil && hostErr.Code == abiv1.ErrCodeNotFound && hasField(dep.ModelDecl, "deleted_at") {
			continue
		}
		if hostErr != nil {
			return hostErr
		}
		for _, field := range group.deps {
			value, hostErr := invokeCompute(ctx, r, modCtx, tx, field, before)
			if hostErr != nil {
				return computeFailure(field, group.id, hostErr)
			}
			if hostErr := applyComputedValue(ctx, tx, field.ModelDecl, pk, group.id, field.Field, value); hostErr != nil {
				return hostErr
			}
		}
		after, hostErr := fetchStoredRowByPK(ctx, tx, dep.ModelDecl, pk, group.id)
		if hostErr != nil {
			return hostErr
		}
		var changed []string
		for _, field := range group.deps {
			if !reflect.DeepEqual(before[field.Field], after[field.Field]) {
				changed = append(changed, field.Field)
			}
		}
		if len(changed) == 0 {
			continue
		}
		slices.Sort(changed)
		qualified := dep.ModelDecl.QualifiedName(dep.ModuleName)
		if hostErr := writeChangeActivity(ctx, tx, modCtx, qualified, dep.ModelDecl, before, after); hostErr != nil {
			return hostErr
		}
		if err := emitRecordEventPayload(ctx, r.EventInsertClient(), tx, modCtx, "orm.record.updated", abiv1.ORMRecordUpdatedPayload{
			Model:         qualified,
			Record:        after,
			ChangedFields: changed,
		}); err != nil {
			return ormSQLErrorRetryable(err)
		}
		if err := emitDeclaredLifecycleEvent(ctx, r.EventInsertClient(), tx, modCtx, dep.ModuleName, qualified, dep.ModelDecl.OnUpdateEvent, after, changed); err != nil {
			return ormSQLErrorRetryable(err)
		}
	}
	return nil
}

func recomputeParentsAfterChildUnlink(ctx context.Context, tx *sql.Tx, r *Runtime, modCtx *ModuleContext, qualifiedChildModel string, existingRow map[string]any) *abiv1.HostError {
	idx := modCtx.ComputedIndex()
	if idx == nil {
		return nil
	}
	return recomputeChildParents(ctx, tx, r, modCtx, idx.LookupViaChild(qualifiedChildModel, changedFieldNames(existingRow)), existingRow)
}
