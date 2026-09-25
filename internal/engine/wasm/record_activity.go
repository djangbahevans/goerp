package wasm

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/djangbahevans/goerp/internal/engine/computed"
	"github.com/djangbahevans/goerp/internal/engine/recordactivity"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

// Record activity feed capture (record-activity.md §5): a model with
// .Tracked() fields gets a `created` entry per create and a `change` entry
// per write that changes a tracked field, written to record_activity in the
// writing transaction so a rolled-back write leaves no entry behind.

type activityChange struct {
	Field string `json:"field"`
	Old   any    `json:"old"`
	New   any    `json:"new"`
}

type activityEntry struct {
	RecordID any
	Kind     string
	Changes  []activityChange
}

// trackedFields returns md's .Tracked() fields in declaration order.
func trackedFields(md model.ModelDeclaration) []model.NamedField {
	var fields []model.NamedField
	for _, f := range md.Fields {
		if f.Def.IsTracked {
			fields = append(fields, f)
		}
	}
	return fields
}

func hasTrackedComputedField(md model.ModelDeclaration) bool {
	return slices.ContainsFunc(md.Fields, func(f model.NamedField) bool { return f.Def.IsTracked && f.Def.IsComputed })
}

func hasTrackedFields(md model.ModelDeclaration) bool {
	return slices.ContainsFunc(md.Fields, func(f model.NamedField) bool { return f.Def.IsTracked })
}

// needsRowBeforeWrite reports whether a write to qualifiedModel needs the
// row's pre-write values: for its audit_log old_data, its change entries,
// or both.
func needsRowBeforeWrite(modCtx *ModuleContext, qualifiedModel string, md model.ModelDeclaration) bool {
	return isAuditedModel(modCtx, qualifiedModel) || hasTrackedFields(md)
}

// writeCreatedActivity writes row's `created` entry when md has tracked
// fields; a no-op otherwise.
func writeCreatedActivity(ctx context.Context, tx *sql.Tx, modCtx *ModuleContext, qualifiedModel string, md model.ModelDeclaration, row map[string]any) *abi.HostError {
	if !hasTrackedFields(md) {
		return nil
	}
	pkCol, ok := primaryKeyColumn(md)
	if !ok {
		return nil
	}
	if err := insertActivityEntries(ctx, tx, modCtx, qualifiedModel, []activityEntry{{RecordID: row[pkCol], Kind: recordactivity.KindCreated}}); err != nil {
		return ormSQLError(err)
	}
	return nil
}

// writeChangeActivity writes one `change` entry listing every tracked field
// whose value differs between oldRow and newRow. A write that changes no
// tracked field, or a missing oldRow, writes nothing.
func writeChangeActivity(ctx context.Context, tx *sql.Tx, modCtx *ModuleContext, qualifiedModel string, md model.ModelDeclaration, oldRow, newRow map[string]any) *abi.HostError {
	if oldRow == nil || newRow == nil || !hasTrackedFields(md) {
		return nil
	}
	pkCol, ok := primaryKeyColumn(md)
	if !ok {
		return nil
	}
	if hasTrackedComputedField(md) {
		// A same-record recompute patches newRow with the compute
		// function's own return value, whose Go type can differ from the
		// scanned column's; compare against the stored row instead.
		fresh, hostErr := fetchRowByPK(ctx, tx, md, pkCol, newRow[pkCol])
		if hostErr != nil {
			return hostErr
		}
		newRow = fresh
	}
	changes, err := trackedChanges(md, oldRow, newRow)
	if err != nil {
		return &abi.HostError{Code: abi.ErrCodeUnavailable, Message: err.Error()}
	}
	if len(changes) == 0 {
		return nil
	}
	if err := insertActivityEntries(ctx, tx, modCtx, qualifiedModel, []activityEntry{{RecordID: newRow[pkCol], Kind: recordactivity.KindChange, Changes: changes}}); err != nil {
		return ormSQLError(err)
	}
	return nil
}

// trackedChanges compares each tracked field's old and new value in the
// JSON shape host.orm.read returns for it.
func trackedChanges(md model.ModelDeclaration, oldRow, newRow map[string]any) ([]activityChange, error) {
	var changes []activityChange
	for _, f := range trackedFields(md) {
		oldVal := activityValue(f.Def, oldRow[f.Name])
		newVal := activityValue(f.Def, newRow[f.Name])
		oldJSON, err := json.Marshal(oldVal)
		if err != nil {
			return nil, fmt.Errorf("encode old value of %s: %w", f.Name, err)
		}
		newJSON, err := json.Marshal(newVal)
		if err != nil {
			return nil, fmt.Errorf("encode new value of %s: %w", f.Name, err)
		}
		if bytes.Equal(oldJSON, newJSON) {
			continue
		}
		changes = append(changes, activityChange{Field: f.Name, Old: oldVal, New: newVal})
	}
	return changes, nil
}

// activityValue converts a scanned column value to the JSON value a change
// entry stores: a Date as an ISO 8601 date rather than a midnight
// timestamp, and a JSONB column as its JSON document rather than bytes.
func activityValue(def model.FieldDef, v any) any {
	switch val := v.(type) {
	case time.Time:
		if def.Kind == model.KindDate {
			return val.Format(time.DateOnly)
		}
	case []byte:
		if def.Kind == model.KindJSONB && jsontext.Value(val).IsValid() {
			return jsontext.Value(bytes.Clone(val))
		}
	}
	return v
}

// maxActivityWriteChunkRows keeps each multi-row INSERT's bound parameters
// (7 per row) well under Postgres's 65535-parameter limit.
const maxActivityWriteChunkRows = 700

// insertActivityEntries writes entries for qualifiedModel, authored by the
// request's user (NULL with no user in context).
func insertActivityEntries(ctx context.Context, tx *sql.Tx, modCtx *ModuleContext, qualifiedModel string, entries []activityEntry) error {
	const paramsPerRow = 7
	for start := 0; start < len(entries); start += maxActivityWriteChunkRows {
		chunk := entries[start:min(start+maxActivityWriteChunkRows, len(entries))]
		placeholders := make([]string, len(chunk))
		args := make([]any, 0, len(chunk)*paramsPerRow)
		for i, e := range chunk {
			var changesJSON any
			if e.Kind == recordactivity.KindChange {
				encoded, err := json.Marshal(e.Changes)
				if err != nil {
					return fmt.Errorf("encode activity changes: %w", err)
				}
				changesJSON = encoded
			}
			base := i * paramsPerRow
			placeholders[i] = fmt.Sprintf("($%d, $%d, $%d, $%d::jsonb, NULLIF($%d, '')::uuid, NULLIF($%d, ''), NULLIF($%d, ''))",
				base+1, base+2, base+3, base+4, base+5, base+6, base+7)
			args = append(args, qualifiedModel, e.RecordID, e.Kind, changesJSON, modCtx.UserID, modCtx.RequestID, modCtx.TraceID)
		}
		sqlStr := fmt.Sprintf(`INSERT INTO %s (model, record_id, kind, changes, author_id, request_id, trace_id) VALUES %s`,
			quoteIdentORM(recordactivity.TableName), strings.Join(placeholders, ", "))
		if _, err := tx.ExecContext(ctx, sqlStr, args...); err != nil {
			return fmt.Errorf("insert record_activity rows: %w", err)
		}
	}
	return nil
}

// resolveTrackedExecTable resolves table (a bare name from raw SQL) to the
// caller's own declared model, when that model has tracked fields.
func resolveTrackedExecTable(modCtx *ModuleContext, table string) (md model.ModelDeclaration, qualifiedModel, pkCol string, ok bool) {
	for _, decl := range modCtx.ModelDecls() {
		if tableNameForORM(decl) != table {
			continue
		}
		if !hasTrackedFields(decl) {
			return model.ModelDeclaration{}, "", "", false
		}
		pk, hasPK := primaryKeyColumn(decl)
		if !hasPK {
			return model.ModelDeclaration{}, "", "", false
		}
		return decl, decl.QualifiedName(modCtx.ModuleName), pk, true
	}
	return model.ModelDeclaration{}, "", "", false
}

// writeExecChangeActivity writes one change entry per row a raw-SQL UPDATE
// changed a tracked field on, pairing old and new rows the same way the
// audit log does (pairAuditEntries).
func writeExecChangeActivity(ctx context.Context, tx *sql.Tx, modCtx *ModuleContext, qualifiedModel string, md model.ModelDeclaration, pkCol string, oldRows, newRows []map[string]any) error {
	var entries []activityEntry
	for _, pair := range pairAuditEntries(pkCol, oldRows, newRows) {
		if pair.OldData == nil || pair.NewData == nil {
			continue
		}
		changes, err := trackedChanges(md, pair.OldData, pair.NewData)
		if err != nil {
			return err
		}
		if len(changes) == 0 {
			continue
		}
		entries = append(entries, activityEntry{RecordID: pair.NewData[pkCol], Kind: recordactivity.KindChange, Changes: changes})
	}
	return insertActivityEntries(ctx, tx, modCtx, qualifiedModel, entries)
}

// lockConflictRowBeforeUpsert reads, and locks, the existing row an
// OnConflictUpdate create would overwrite, so its change entry has old
// values to compare against. nil when md has no tracked fields, the create
// isn't an OnConflictUpdate, or no row matches the conflict target yet.
func lockConflictRowBeforeUpsert(ctx context.Context, tx *sql.Tx, md model.ModelDeclaration, qualifiedModel string, record map[string]any, onConflict *OnConflictOption) (map[string]any, *abi.HostError) {
	if onConflict == nil || onConflict.Policy != "update" || !hasTrackedFields(md) {
		return nil, nil
	}
	targetCols, hostErr := validateOnConflictTarget(md, qualifiedModel, onConflict.Fields, "on_conflict.fields")
	if hostErr != nil {
		return nil, hostErr
	}
	conds := make([]string, len(targetCols))
	args := make([]any, len(targetCols))
	for i, col := range targetCols {
		v, ok := record[col]
		if !ok || v == nil {
			return nil, nil
		}
		conds[i] = fmt.Sprintf("%s = $%d", quoteIdentORM(col), i+1)
		args[i] = v
	}
	sqlStr := fmt.Sprintf("SELECT * FROM %s WHERE %s FOR UPDATE", quoteIdentORM(tableNameForORM(md)), strings.Join(conds, " AND "))
	rows, err := tx.QueryContext(ctx, sqlStr, args...)
	if err != nil {
		return nil, ormSQLError(err)
	}
	found, err := scanRowsToMaps(rows)
	if err != nil {
		return nil, ormSQLError(err)
	}
	if len(found) != 1 {
		return nil, nil
	}
	return found[0], nil
}

// writeCreateActivity writes the feed entry for one create_one result: a
// `created` entry for an inserted row, or a change entry for a row an
// OnConflictUpdate overwrote.
func writeCreateActivity(ctx context.Context, tx *sql.Tx, modCtx *ModuleContext, qualifiedModel string, md model.ModelDeclaration, inserted bool, conflictRow, row map[string]any) *abi.HostError {
	if inserted {
		return writeCreatedActivity(ctx, tx, modCtx, qualifiedModel, md, row)
	}
	return writeChangeActivity(ctx, tx, modCtx, qualifiedModel, md, conflictRow, row)
}

// writeRecomputedActivity writes the change entry for a stored computed
// field recomputed on another record (a Many2One or One2Many hop), when that
// field is tracked. oldRow is the dependent row as read before the recompute.
func writeRecomputedActivity(ctx context.Context, tx *sql.Tx, modCtx *ModuleContext, dep computed.Dependent, oldRow map[string]any) *abi.HostError {
	if !isTrackedField(dep.ModelDecl, dep.Field) {
		return nil
	}
	// dep.Field is a tracked computed field, so writeChangeActivity re-reads
	// the stored row as the new side; oldRow only supplies the primary key.
	return writeChangeActivity(ctx, tx, modCtx, dep.ModelDecl.QualifiedName(dep.ModuleName), dep.ModelDecl, oldRow, oldRow)
}

func isTrackedField(md model.ModelDeclaration, name string) bool {
	return slices.ContainsFunc(md.Fields, func(f model.NamedField) bool { return f.Name == name && f.Def.IsTracked })
}
