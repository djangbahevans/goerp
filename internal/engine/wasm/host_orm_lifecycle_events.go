package wasm

import (
	"context"
	"database/sql"
	"uuid"

	"github.com/riverqueue/river"
	"github.com/vmihailenco/msgpack/v5"

	"github.com/djangbahevans/goerp/sdk/go/model"
)

// emitLifecycleEvent emits the model's OnCreate/OnUpdate/OnDelete event for
// a write, if it declared one, through the same outbox as orm.record.*
// and in the write's transaction. The payload holds the declared fields
// of record, masked per the field security rules the actor's own reads
// follow; changedFields fills a declared changed_fields payload field.
func emitLifecycleEvent(ctx context.Context, insertClient *river.Client[*sql.Tx], tx *sql.Tx, modCtx *ModuleContext, modelName string, pick func(model.ModelDeclaration) *model.LifecycleEvent, record map[string]any, changedFields []string) error {
	md, ok := resolveModel(modCtx, modelName)
	if !ok {
		return nil
	}
	ev := pick(md)
	if ev == nil {
		return nil
	}

	// Masked on the record's own field names, before payload keys are applied.
	selected := make(map[string]any, len(ev.Fields))
	for _, field := range ev.Fields {
		if v, ok := record[field.Record]; ok {
			selected[field.Record] = v
		}
	}
	applyFieldMasking(modCtx, modelName, []map[string]any{selected})

	body := make(map[string]any, len(ev.Fields)+1)
	for _, field := range ev.Fields {
		if v, ok := selected[field.Record]; ok {
			body[field.Name] = v
		}
	}
	selected = body
	if ev.ChangedFields {
		if changedFields == nil {
			changedFields = []string{}
		}
		selected["changed_fields"] = changedFields
	}

	payload, err := msgpack.Marshal(selected)
	if err != nil {
		return err
	}
	return insertEventDeliveryTx(ctx, insertClient, tx, uuid.NewV7(), ev.Name, ev.Version,
		modCtx.ModuleName, modCtx.TenantID, modCtx.UserID, modCtx.TraceID, payload, 0, nil)
}

func onCreateEvent(md model.ModelDeclaration) *model.LifecycleEvent { return md.OnCreateEvent }
func onUpdateEvent(md model.ModelDeclaration) *model.LifecycleEvent { return md.OnUpdateEvent }
func onDeleteEvent(md model.ModelDeclaration) *model.LifecycleEvent { return md.OnDeleteEvent }
