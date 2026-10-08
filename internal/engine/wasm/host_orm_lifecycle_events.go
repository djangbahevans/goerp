package wasm

import (
	"context"
	"database/sql"
	"uuid"

	"github.com/riverqueue/river"
	"github.com/vmihailenco/msgpack/v5"

	"github.com/djangbahevans/goerp/sdk/go/model"
)

// Lifecycle payloads follow the actor's read access rules even though the
// write's audit data and orm.record events retain unmasked values.
func emitLifecycleEvent(ctx context.Context, insertClient *river.Client[*sql.Tx], tx *sql.Tx, modCtx *ModuleContext, modelName string, pick func(model.ModelDeclaration) *model.LifecycleEvent, record map[string]any, changedFields []string) error {
	md, ok := resolveModel(modCtx, modelName)
	if !ok {
		return nil
	}
	ev := pick(md)
	if ev == nil {
		return nil
	}

	// Apply access rules to source field names before mapping them to payload keys.
	selected := make(map[string]any, len(ev.Fields))
	for _, field := range ev.Fields {
		if v, ok := record[field.Record]; ok {
			selected[field.Record] = v
		}
	}
	applyFieldMasking(modCtx, modelName, []map[string]any{selected})
	payload, err := recordEventPayload(ev, selected, changedFields)
	if err != nil {
		return err
	}
	return insertEventDeliveryTx(ctx, insertClient, tx, uuid.NewV7(), ev.Name, ev.Version,
		modCtx.ModuleName, modCtx.TenantID, modCtx.UserID, modCtx.TraceID, payload, 0, nil)
}

func recordEventPayload(ev *model.LifecycleEvent, record map[string]any, changedFields []string) ([]byte, error) {
	body := make(map[string]any, len(ev.Fields)+1)
	for _, field := range ev.Fields {
		if value, ok := record[field.Record]; ok {
			body[field.Name] = value
		}
	}
	if ev.ChangedFields {
		if changedFields == nil {
			changedFields = []string{}
		}
		body["changed_fields"] = changedFields
	}

	return msgpack.Marshal(body)
}

func onCreateEvent(md model.ModelDeclaration) *model.LifecycleEvent { return md.OnCreateEvent }
func onUpdateEvent(md model.ModelDeclaration) *model.LifecycleEvent { return md.OnUpdateEvent }
func onDeleteEvent(md model.ModelDeclaration) *model.LifecycleEvent { return md.OnDeleteEvent }
