package wasm

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"
	"uuid"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

// newORMCreateChangedFieldsModuleContext is newORMWriteTestModuleContext
// with a UUID-valid UserID, so fillCreateServerFields actually fills
// created_by (it parses modCtx.UserID as a UUID before filling; the
// package's usual "user-1" fixture UserID never satisfies that check).
// TenantID is a freshly generated UUID (goerp#992 made tenant_id
// Readonly, so — same as newORMWriteTestModuleContext — it can no longer
// be the non-UUID slug, since fillCreateServerFields now writes it
// straight into the real tenant_id column when a create omits it), kept
// distinct from TenantSlug; a caller filtering river_job by tenant_id
// for isolation (updatedEventPayloads/countEventDeliveryJobsByName) uses
// the returned UUID, not the slug.
func newORMCreateChangedFieldsModuleContext(slug string, decls []model.ModelDeclaration) (mc *ModuleContext, tenantID string) {
	tenantID = uuid.New().String()
	return NewModuleContext("req-1", "testmodule", "00000000-0000-0000-0000-0000000000aa", "contact-1", []string{"admin"}, nil,
		tenantID, slug, "trace-1", abi.CapDBRead|abi.CapDBWrite, nil, ModuleSnapshot{ModelDecls: decls}), tenantID
}

func TestHostORM_Create_OnConflictUpdate_EmitsChangedFields(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("ormconflictchanged%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	createFixtureItemsTable(t, primaryDB, slug)

	r := newHostDBTestRuntime(t, primaryDB, 10)
	mc, tenantID := newORMCreateChangedFieldsModuleContext(slug, []model.ModelDeclaration{itemModelDecl()})
	insertClient := r.EventInsertClient()

	first := abiv1.ORMCreateInput{Model: "testmodule.item", Record: map[string]any{
		"name": "A", "code": "DUP3",
	}}
	if _, hostErr := ORMCreate(ctx, r, primaryDB, insertClient, nil, mc, first); hostErr != nil {
		t.Fatalf("first create failed: %+v", hostErr)
	}

	// created_by is omitted here deliberately — fillCreateServerFields
	// fills it from modCtx.UserID, so it must not show up as a changed
	// field on the update arm. id/tenant_id are now Readonly (goerp#992)
	// and are always engine-filled instead of supplied.
	second := abiv1.ORMCreateInput{
		Model: "testmodule.item",
		Record: map[string]any{
			"name": "B updated", "code": "DUP3",
		},
		OnConflict: &abiv1.ORMOnConflict{Fields: []string{"code"}, Policy: "update"},
	}
	if _, hostErr := ORMCreate(ctx, r, primaryDB, insertClient, nil, mc, second); hostErr != nil {
		t.Fatalf("OnConflictUpdate create failed: %+v", hostErr)
	}

	events := updatedEventPayloads(t, primaryDB, tenantID)
	if len(events) != 1 {
		t.Fatalf("orm.record.updated events = %d, want 1", len(events))
	}
	if !slices.Contains(events[0].ChangedFields, "name") {
		t.Errorf("changed_fields = %v, want it to include name", events[0].ChangedFields)
	}
	if slices.Contains(events[0].ChangedFields, "created_by") {
		t.Errorf("changed_fields = %v, want it to exclude the server-filled created_by", events[0].ChangedFields)
	}
}

func TestHostORM_CreateBatch_OnConflictUpdate_OneEventPerUpdatedRowWithOwnChangedFields(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("ormcreatebatchchanged%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	createFixtureItemsTable(t, primaryDB, slug)

	r := newHostDBTestRuntime(t, primaryDB, 10)
	mc, tenantID := newORMCreateChangedFieldsModuleContext(slug, []model.ModelDeclaration{itemModelDecl()})
	insertClient := r.EventInsertClient()

	seed := abiv1.ORMCreateBatchInput{Model: "testmodule.item", Records: []map[string]any{
		{"name": "A", "code": "BATCH-A"},
		{"name": "B", "code": "BATCH-B"},
	}}
	if _, hostErr := ORMCreateBatch(ctx, r, primaryDB, insertClient, mc, seed); hostErr != nil {
		t.Fatalf("seed create_batch failed: %+v", hostErr)
	}

	// id/tenant_id are Readonly (goerp#992) and always engine-assigned —
	// the OnConflict target is "code", not "id", so the update rows below
	// don't need to know or repeat the seed rows' generated ids; only
	// "name"/"number" distinguish the two update rows from each other for
	// the purpose of this assertion.
	upsert := abiv1.ORMCreateBatchInput{
		Model: "testmodule.item",
		Records: []map[string]any{
			{"name": "C", "code": "BATCH-C"}, // new insert
			{"name": "A renamed", "code": "BATCH-A"},
			{"name": "B", "code": "BATCH-B", "number": int64(9)},
		},
		OnConflict: &abiv1.ORMOnConflict{Fields: []string{"code"}, Policy: "update"},
	}
	if _, hostErr := ORMCreateBatch(ctx, r, primaryDB, insertClient, mc, upsert); hostErr != nil {
		t.Fatalf("upsert create_batch failed: %+v", hostErr)
	}

	if got := countEventDeliveryJobsByName(t, primaryDB, "orm.record.created", tenantID); got != 2 {
		t.Errorf("orm.record.created jobs = %d, want 2 (one batched event for each create_batch call)", got)
	}

	events := updatedEventPayloads(t, primaryDB, tenantID)
	if len(events) != 2 {
		t.Fatalf("orm.record.updated events = %d, want 2 (one per updated row, not one batched)", len(events))
	}
	byCode := make(map[string][]string, 2)
	for _, e := range events {
		byCode[fmt.Sprint(e.Record["code"])] = e.ChangedFields
	}
	if !slices.Contains(byCode["BATCH-A"], "name") {
		t.Errorf("BATCH-A changed_fields = %v, want it to include name", byCode["BATCH-A"])
	}
	if !slices.Contains(byCode["BATCH-B"], "number") {
		t.Errorf("BATCH-B changed_fields = %v, want it to include number", byCode["BATCH-B"])
	}
}
