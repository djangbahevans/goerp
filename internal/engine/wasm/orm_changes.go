package wasm

import (
	"context"
	"database/sql"
	"fmt"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
)

type RecordChangePublisher interface {
	RecordChanged(ctx context.Context, tenantID, resource, recordID, operation string)
}

func (r *Runtime) SetRecordChangePublisher(publisher RecordChangePublisher) {
	r.recordChanges = publisher
}

type ormChange struct {
	resource  string
	recordID  string
	operation string
}

type ormChangeBatch struct {
	publisher RecordChangePublisher
	tenantID  string
	changes   []ormChange
}

func (b *ormChangeBatch) publish(ctx context.Context) {
	if b == nil {
		return
	}
	for _, change := range b.changes {
		b.publisher.RecordChanged(ctx, b.tenantID, change.resource, change.recordID, change.operation)
	}
}

func (mc *ModuleContext) trackORMChanges(r *Runtime, tx *sql.Tx, txID string) *ormChangeBatch {
	if r == nil || r.recordChanges == nil {
		return nil
	}

	mc.txMu.Lock()
	defer mc.txMu.Unlock()

	if batch := mc.ormChanges[tx]; batch != nil {
		return batch
	}
	batch := &ormChangeBatch{publisher: r.recordChanges, tenantID: mc.TenantID}
	if mc.ormChanges == nil {
		mc.ormChanges = make(map[*sql.Tx]*ormChangeBatch)
	}
	mc.ormChanges[tx] = batch
	if txID != "" {
		ot := mc.transactions[txID]
		ot.afterCommit = append(ot.afterCommit, batch.publish)
		mc.transactions[txID] = ot
	}
	return batch
}

func (mc *ModuleContext) queueORMChange(tx *sql.Tx, payload any) {
	mc.txMu.Lock()
	defer mc.txMu.Unlock()

	batch := mc.ormChanges[tx]
	if batch == nil {
		return
	}
	var change ormChange
	switch p := payload.(type) {
	case abiv1.ORMRecordCreatedPayload:
		change = ormChange{resource: p.Model, operation: "created"}
	case abiv1.ORMRecordUpdatedPayload:
		md, ok := resolveModel(mc, p.Model)
		if !ok {
			return
		}
		pk, ok := primaryKeyColumn(md)
		if !ok || p.Record[pk] == nil {
			return
		}
		change = ormChange{resource: p.Model, recordID: fmt.Sprint(p.Record[pk]), operation: "updated"}
	case abiv1.ORMRecordDeletedPayload:
		change = ormChange{resource: p.Model, recordID: fmt.Sprint(p.Record["id"]), operation: "deleted"}
	default:
		return
	}
	batch.changes = append(batch.changes, change)
}
