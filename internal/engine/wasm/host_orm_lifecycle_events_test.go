package wasm

import (
	"context"
	"fmt"
	"testing"
	"time"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/sdk/go/events/def"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

type itemCreatedPayload struct {
	ItemID string `msgpack:"item_id" record:"id"`
	Name   string `msgpack:"name"`
}

type itemUpdatedPayload struct {
	ID            string   `msgpack:"id"`
	Name          string   `msgpack:"name"`
	ChangedFields []string `msgpack:"changed_fields"`
}

type itemDeletedPayload struct {
	Name string `msgpack:"name"`
	Code string `msgpack:"code"`
}

var (
	itemCreated = def.Define[itemCreatedPayload]("testmodule.item.created")
	itemUpdated = def.Define[itemUpdatedPayload]("testmodule.item.updated", def.Version(2))
	itemDeleted = def.Define[itemDeletedPayload]("testmodule.item.deleted")
)

func lifecycleItemModelDecl() model.ModelDeclaration {
	d := itemModelDecl()
	d.OnCreate(itemCreated).OnUpdate(itemUpdated).OnDelete(itemDeleted)
	return d
}

func TestORMLifecycleEvents_CreateUpdateDeleteEmitDeclaredPayloads(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("ormlifecycle%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	createFixtureItemsTable(t, primaryDB, slug)

	r := newHostDBTestRuntime(t, primaryDB, 10)
	mc, tenantID := newORMWriteTestModuleContext(slug, []model.ModelDeclaration{lifecycleItemModelDecl()})
	insertClient := r.EventInsertClient()

	out, hostErr := ORMCreate(ctx, r, primaryDB, insertClient, nil, mc, abiv1.ORMCreateInput{
		Model: "testmodule.item", Record: map[string]any{"name": "A", "code": "c-1"},
	})
	if hostErr != nil {
		t.Fatalf("ORMCreate: %+v", hostErr)
	}
	id, _ := out.Record["id"].(string)

	if got := countEventDeliveryJobsByName(t, primaryDB, "testmodule.item.created", tenantID); got != 1 {
		t.Fatalf("created events = %d, want 1", got)
	}
	var created itemCreatedPayload
	mustParsePayload(t, "created", rawEventPayload(t, primaryDB, "testmodule.item.created", tenantID), &created)
	if created.ItemID != id || created.Name != "A" {
		t.Errorf("created payload = %+v, want item_id %s (read from the record's id) and name A", created, id)
	}

	if _, hostErr := ORMWrite(ctx, r, primaryDB, insertClient, nil, mc, abiv1.ORMWriteInput{
		Model: "testmodule.item", ID: id, Record: map[string]any{"name": "A2"},
	}); hostErr != nil {
		t.Fatalf("ORMWrite: %+v", hostErr)
	}

	if got := countEventDeliveryJobsByName(t, primaryDB, "testmodule.item.updated", tenantID); got != 1 {
		t.Fatalf("updated events = %d, want 1", got)
	}
	var updated itemUpdatedPayload
	mustParsePayload(t, "updated", rawEventPayload(t, primaryDB, "testmodule.item.updated", tenantID), &updated)
	if updated.Name != "A2" || len(updated.ChangedFields) != 1 || updated.ChangedFields[0] != "name" {
		t.Errorf("updated payload = %+v, want name A2 and ChangedFields [name]", updated)
	}
	var version int
	if err := primaryDB.QueryRow(
		`SELECT (args->>'event_version')::int FROM system.river_job WHERE kind = 'event_delivery' AND args->>'event_name' = 'testmodule.item.updated' AND args->>'tenant_id' = $1`,
		tenantID).Scan(&version); err != nil {
		t.Fatalf("read event version: %v", err)
	}
	if version != 2 {
		t.Errorf("updated event version = %d, want the definition's 2", version)
	}

	if _, hostErr := ORMUnlink(ctx, r, primaryDB, insertClient, nil, mc, abiv1.ORMUnlinkInput{
		Model: "testmodule.item", IDs: []string{id},
	}); hostErr != nil {
		t.Fatalf("ORMUnlink: %+v", hostErr)
	}

	if got := countEventDeliveryJobsByName(t, primaryDB, "testmodule.item.deleted", tenantID); got != 1 {
		t.Fatalf("deleted events = %d, want 1", got)
	}
	var deleted itemDeletedPayload
	mustParsePayload(t, "deleted", rawEventPayload(t, primaryDB, "testmodule.item.deleted", tenantID), &deleted)
	if deleted.Name != "A2" || deleted.Code != "c-1" {
		t.Errorf("deleted payload = %+v, want the pre-deletion record's name A2 and code c-1", deleted)
	}
}

func TestORMLifecycleEvents_CreateBatchEmitsOnePerRecord(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("ormlifecyclebatch%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	createFixtureItemsTable(t, primaryDB, slug)

	r := newHostDBTestRuntime(t, primaryDB, 10)
	mc, tenantID := newORMWriteTestModuleContext(slug, []model.ModelDeclaration{lifecycleItemModelDecl()})

	if _, hostErr := ORMCreateBatch(ctx, r, primaryDB, r.EventInsertClient(), mc, abiv1.ORMCreateBatchInput{
		Model: "testmodule.item", Records: []map[string]any{{"name": "A"}, {"name": "B"}},
	}); hostErr != nil {
		t.Fatalf("ORMCreateBatch: %+v", hostErr)
	}

	if got := countEventDeliveryJobsByName(t, primaryDB, "testmodule.item.created", tenantID); got != 2 {
		t.Errorf("created events = %d, want 2", got)
	}
}

func TestORMLifecycleEvents_RolledBackWriteEmitsNothing(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("ormlifecyclerb%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	createFixtureItemsTable(t, primaryDB, slug)

	r := newHostDBTestRuntime(t, primaryDB, 10)
	mc, tenantID := newORMWriteTestModuleContext(slug, []model.ModelDeclaration{lifecycleItemModelDecl()})

	const txID = "lifecycle-tx"
	tx := registerTenantScopedTestTx(t, ctx, primaryDB, mc, txID)

	if _, hostErr := ORMCreate(ctx, r, primaryDB, r.EventInsertClient(), nil, mc, abiv1.ORMCreateInput{
		Model: "testmodule.item", Record: map[string]any{"name": "A"}, TxID: txID,
	}); hostErr != nil {
		t.Fatalf("ORMCreate: %+v", hostErr)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatalf("rollback: %v", err)
	}

	if got := countEventDeliveryJobsByName(t, primaryDB, "testmodule.item.created", tenantID); got != 0 {
		t.Errorf("created events after rollback = %d, want 0", got)
	}
}

func TestORMLifecycleEvents_UndeclaredEventsAreNotEmitted(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("ormlifecyclenone%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	createFixtureItemsTable(t, primaryDB, slug)

	r := newHostDBTestRuntime(t, primaryDB, 10)
	mc, tenantID := newORMWriteTestModuleContext(slug, []model.ModelDeclaration{itemModelDecl()})

	if _, hostErr := ORMCreate(ctx, r, primaryDB, r.EventInsertClient(), nil, mc, abiv1.ORMCreateInput{
		Model: "testmodule.item", Record: map[string]any{"name": "A"},
	}); hostErr != nil {
		t.Fatalf("ORMCreate: %+v", hostErr)
	}

	if got := countEventDeliveryJobsByName(t, primaryDB, "testmodule.item.created", tenantID); got != 0 {
		t.Errorf("created events = %d, want 0 for a model with no OnCreate", got)
	}
}
