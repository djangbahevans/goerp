package wasm

import (
	"context"
	"database/sql"
	"fmt"
	"maps"
	"slices"
	"strings"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/fieldsec"
	"github.com/djangbahevans/goerp/internal/engine/modelmeta"
	"github.com/djangbahevans/goerp/sdk/go/model"
	"github.com/riverqueue/river"
)

func composedReadColumns(mc *ModuleContext, name string, md model.ModelDeclaration, requested []string) ([]string, *abiv1.HostError) {
	fields, err := modelmeta.ComposedFields(md, func(name string) (model.ModelDeclaration, bool) {
		return resolveReadableModel(mc, name)
	})
	if err != nil {
		return nil, &abiv1.HostError{Code: abiv1.ErrCodeValidationFailed, Message: err.Error()}
	}
	md.Fields = fields
	return readableColumns(name, md, requested)
}

func delegateQueryColumns(md model.ModelDeclaration, columns []string, pk string) []string {
	own := make([]string, 0, len(columns))
	for _, name := range columns {
		if hasField(md, name) {
			own = append(own, name)
		}
	}
	for _, f := range md.Fields {
		if f.Def.Kind == model.KindDelegate && !slices.Contains(own, f.Name) {
			own = append(own, f.Name)
		}
	}
	return relationQueryColumns(md, own, pk)
}

func expandDelegates(ctx context.Context, tx *sql.Tx, mc *ModuleContext, name string, md model.ModelDeclaration, columns []string, records []map[string]any, mask bool) *abiv1.HostError {
	for _, f := range md.Fields {
		if f.Def.Kind != model.KindDelegate {
			continue
		}
		if mask && !delegateFieldReadable(mc, name, f.Name) {
			continue
		}
		target, ok := resolveReadableModel(mc, f.Def.RelatedModel)
		if !ok {
			continue
		}
		pk, ok := primaryKeyColumn(target)
		if !ok {
			continue
		}
		fields, hostErr := composedReadColumns(mc, f.Def.RelatedModel, target, nil)
		if hostErr != nil {
			return hostErr
		}
		selected := slices.DeleteFunc(fields, func(field string) bool {
			return hasField(md, field) || !slices.Contains(columns, field)
		})
		if len(selected) == 0 {
			continue
		}

		ids := distinctNonNilStrings(records, f.Name)
		if len(ids) == 0 {
			continue
		}
		_, byID, hostErr := ormReadKeyedTx(ctx, tx, mc, abiv1.ORMReadInput{Model: f.Def.RelatedModel, IDs: ids}, target, pk, selected, !mask)
		if hostErr != nil {
			return hostErr
		}
		for _, record := range records {
			for key, value := range byID[fmt.Sprint(record[f.Name])] {
				if !hasField(md, key) {
					record[key] = value
				}
			}
		}
	}
	return nil
}

func delegateFieldReadable(mc *ModuleContext, name, field string) bool {
	if reg := mc.FieldSecRegistry(); reg != nil {
		if rule, ok := reg.Rule(name, field); ok {
			return fieldReadAllowed(mc, mc.PermissionRegistry(), rule)
		}
	}
	return true
}

func splitDelegateValues(mc *ModuleContext, name string, md model.ModelDeclaration, values map[string]any) (map[string]any, map[string]map[string]any, *abiv1.HostError) {
	own := maps.Clone(values)
	delegated := map[string]map[string]any{}
	if _, err := modelmeta.ComposedFields(md, func(name string) (model.ModelDeclaration, bool) {
		return resolveReadableModel(mc, name)
	}); err != nil {
		return nil, nil, &abiv1.HostError{Code: abiv1.ErrCodeValidationFailed, Message: err.Error()}
	}
	for _, f := range md.Fields {
		if f.Def.Kind != model.KindDelegate {
			continue
		}
		rule, denied := writeDeniedBy(mc, name, f.Name)
		if denied && rule.OnDeniedWrite == fieldsec.Ignore {
			delete(own, f.Name)
		}
		target, ok := resolveReadableModel(mc, f.Def.RelatedModel)
		if !ok {
			continue
		}
		fields, hostErr := composedReadColumns(mc, f.Def.RelatedModel, target, nil)
		if hostErr != nil {
			return nil, nil, hostErr
		}
		for _, field := range fields {
			if hasField(md, field) {
				continue
			}
			if value, present := values[field]; present {
				delete(own, field)
				if denied {
					if rule.OnDeniedWrite == fieldsec.Ignore {
						continue
					}
					return nil, nil, &abiv1.HostError{Code: abiv1.ErrCodeFieldWriteDenied, Message: "delegation requires write access to " + f.Name}
				}
				if delegated[f.Name] == nil {
					delegated[f.Name] = map[string]any{}
				}
				delegated[f.Name][field] = value
			}
		}
	}
	return own, delegated, nil
}

func createDelegateTargets(ctx context.Context, r *Runtime, tx *sql.Tx, events *river.Client[*sql.Tx], mc *ModuleContext, md model.ModelDeclaration, name string, record map[string]any, conflict *abiv1.ORMOnConflict) *abiv1.HostError {
	own, delegated, hostErr := splitDelegateValues(mc, name, md, record)
	if hostErr != nil {
		return hostErr
	}
	clear(record)
	maps.Copy(record, own)
	for _, f := range md.Fields {
		if f.Def.Kind != model.KindDelegate {
			continue
		}
		if record[f.Name] == nil && len(delegated[f.Name]) == 0 {
			continue
		}
		if conflict != nil && record[f.Name] == nil && len(delegated[f.Name]) > 0 {
			return &abiv1.HostError{Code: abiv1.ErrCodeValidationFailed, Message: "on_conflict cannot create a delegated target; pass an existing delegate ID"}
		}
		if _, denied := writeDeniedBy(mc, name, f.Name); denied {
			return &abiv1.HostError{Code: abiv1.ErrCodeFieldWriteDenied, Message: "delegation requires write access to " + f.Name}
		}
		target, ok := resolveReadableModel(mc, f.Def.RelatedModel)
		if !ok {
			return &abiv1.HostError{Code: abiv1.ErrCodeModelNotFound, Message: "delegate target " + f.Def.RelatedModel + " is not available"}
		}
		pk, _ := primaryKeyColumn(target)
		if id, linked := record[f.Name]; linked && id != nil {
			if len(delegated[f.Name]) > 0 {
				return &abiv1.HostError{Code: abiv1.ErrCodeValidationFailed, Message: "an existing delegate ID cannot be combined with delegated create fields"}
			}
			if _, hostErr := fetchRowByPK(ctx, tx, target, pk, id); hostErr != nil {
				return hostErr
			}
			continue
		}
		if len(delegated[f.Name]) == 0 {
			continue
		}
		targetCtx := delegateContext(mc, f.Def.RelatedModel, tx)
		out, hostErr := ormCreateRecordTx(ctx, r, tx, events, targetCtx, target, abiv1.ORMCreateInput{Model: f.Def.RelatedModel, Record: delegated[f.Name]})
		if hostErr != nil {
			return hostErr
		}
		record[f.Name] = out.Record[pk]
	}
	return nil
}

func writeDelegateTargets(ctx context.Context, r *Runtime, tx *sql.Tx, events *river.Client[*sql.Tx], mc *ModuleContext, md model.ModelDeclaration, name string, row, values map[string]any, delegated map[string]map[string]any) *abiv1.HostError {
	for _, f := range md.Fields {
		if f.Def.Kind != model.KindDelegate {
			continue
		}
		if _, changed := values[f.Name]; !changed && len(delegated[f.Name]) == 0 {
			continue
		}
		target, ok := resolveReadableModel(mc, f.Def.RelatedModel)
		if !ok {
			return &abiv1.HostError{Code: abiv1.ErrCodeModelNotFound, Message: "delegate target " + f.Def.RelatedModel + " is not available"}
		}
		pk, _ := primaryKeyColumn(target)
		id := row[f.Name]
		if id == nil {
			if len(delegated[f.Name]) > 0 {
				return &abiv1.HostError{Code: abiv1.ErrCodeValidationFailed, Message: "cannot write through an empty Delegate field " + f.Name}
			}
			continue
		}
		if len(delegated[f.Name]) == 0 {
			if _, hostErr := fetchRowByPK(ctx, tx, target, pk, id); hostErr != nil {
				return hostErr
			}
			continue
		}
		if _, denied := writeDeniedBy(mc, name, f.Name); denied {
			return &abiv1.HostError{Code: abiv1.ErrCodeFieldWriteDenied, Message: "delegation requires write access to " + f.Name}
		}
		targetCtx := delegateContext(mc, f.Def.RelatedModel, tx)
		if _, hostErr := ormWriteRecordTx(ctx, r, tx, events, targetCtx, target, pk, abiv1.ORMWriteInput{Model: f.Def.RelatedModel, ID: fmt.Sprint(id), Record: delegated[f.Name]}, nil); hostErr != nil {
			return hostErr
		}
	}
	return nil
}

func delegateContext(mc *ModuleContext, name string, tx *sql.Tx) *ModuleContext {
	owner, _, _ := strings.Cut(name, ".")
	snapshot := mc.snapshot
	if owner != mc.ModuleName {
		snapshot.ModelDecls = mc.ComputeTargets()[owner].ModelDecls
		snapshot.ModuleVersion = mc.ComputeTargets()[owner].Version
	}
	dep := NewModuleContext(mc.RequestID, owner, mc.UserID, mc.ContactID, mc.Roles, mc.PermissionSet,
		mc.TenantID, mc.TenantSlug, mc.TraceID, mc.Capabilities(), mc.txLimiter, snapshot)
	dep.Locale = mc.Locale
	dep.localeResolved = mc.localeResolved
	dep.readTx = tx
	dep.ormChanges = mc.ormChanges
	return dep
}

func composeWriteResult(ctx context.Context, tx *sql.Tx, mc *ModuleContext, name string, md model.ModelDeclaration, row map[string]any) *abiv1.HostError {
	columns, hostErr := composedReadColumns(mc, name, md, nil)
	if hostErr != nil {
		return hostErr
	}
	applyFieldMasking(mc, name, []map[string]any{row})
	return expandDelegates(ctx, tx, mc, name, md, columns, []map[string]any{row}, true)
}

func hasDelegate(md model.ModelDeclaration) bool {
	return slices.ContainsFunc(md.Fields, func(f model.NamedField) bool { return f.Def.Kind == model.KindDelegate })
}

func withDelegateSavepoint(ctx context.Context, tx *sql.Tx, mc *ModuleContext, md model.ModelDeclaration, fn func() *abiv1.HostError) *abiv1.HostError {
	if !hasDelegate(md) {
		return fn()
	}
	mc.txMu.Lock()
	batch := mc.ormChanges[tx]
	count := 0
	if batch != nil {
		count = len(batch.changes)
	}
	mc.txMu.Unlock()

	if _, err := tx.ExecContext(ctx, "SAVEPOINT goerp_delegate"); err != nil {
		return ormSQLError(err)
	}
	hostErr := fn()
	if hostErr != nil {
		_, _ = tx.ExecContext(context.WithoutCancel(ctx), "ROLLBACK TO SAVEPOINT goerp_delegate")
		mc.txMu.Lock()
		if batch != nil {
			batch.changes = batch.changes[:count]
		}
		mc.txMu.Unlock()
	}
	_, err := tx.ExecContext(context.WithoutCancel(ctx), "RELEASE SAVEPOINT goerp_delegate")
	if hostErr != nil {
		return hostErr
	}
	if err != nil {
		return ormSQLError(err)
	}
	return nil
}
