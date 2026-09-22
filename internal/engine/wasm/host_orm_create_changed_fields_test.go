package wasm

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

// newORMCreateChangedFieldsModuleContext is newORMWriteTestModuleContext
// with a UUID-valid UserID, so fillCreateServerFields actually fills
// created_by (it parses modCtx.UserID as a UUID before filling; the
// package's usual "user-1" fixture UserID never satisfies that check).
// TenantID stays the unique slug, like every other test in this package,
// so an orm.record.updated lookup by tenant_id sees only this test's own
// rows against the shared dev Postgres.
func newORMCreateChangedFieldsModuleContext(slug string, decls []model.ModelDeclaration) *ModuleContext {
	return NewModuleContext("req-1", "testmodule", "00000000-0000-0000-0000-0000000000aa", "contact-1", []string{"admin"}, nil,
		slug, slug, "trace-1", abi.CapDBRead|abi.CapDBWrite, nil, ModuleSnapshot{ModelDecls: decls})
}

func TestHostORM_Create_OnConflictUpdate_EmitsChangedFields(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("ormconflictchanged%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	createFixtureItemsTable(t, primaryDB, slug)

	r := newHostDBTestRuntime(t, primaryDB, 10)
	mc := newORMCreateChangedFieldsModuleContext(slug, []model.ModelDeclaration{itemModelDecl()})
	insertClient := r.EventInsertClient()

	first := ORMCreateInput{Model: "testmodule.item", Record: map[string]any{
		"id": "11111111-1111-1111-1111-111111111111", "tenant_id": "00000000-0000-0000-0000-000000000001", "name": "A", "code": "DUP3",
	}}
	if _, hostErr := ORMCreate(ctx, r, primaryDB, insertClient, nil, mc, first); hostErr != nil {
		t.Fatalf("first create failed: %+v", hostErr)
	}

	// created_by is omitted here deliberately — fillCreateServerFields
	// fills it from modCtx.UserID, so it must not show up as a changed
	// field on the update arm. tenant_id is always supplied explicitly:
	// this fixture's modCtx.TenantID is the schema-naming slug, not a
	// UUID, and the column would reject it if fillCreateServerFields had
	// to fill it in.
	second := ORMCreateInput{
		Model: "testmodule.item",
		Record: map[string]any{
			"id": "22222222-2222-2222-2222-222222222222", "tenant_id": "00000000-0000-0000-0000-000000000001",
			"name": "B updated", "code": "DUP3",
		},
		OnConflict: &OnConflictOption{Fields: []string{"code"}, Policy: "update"},
	}
	if _, hostErr := ORMCreate(ctx, r, primaryDB, insertClient, nil, mc, second); hostErr != nil {
		t.Fatalf("OnConflictUpdate create failed: %+v", hostErr)
	}

	events := updatedEventPayloads(t, primaryDB, slug)
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
	mc := newORMCreateChangedFieldsModuleContext(slug, []model.ModelDeclaration{itemModelDecl()})
	insertClient := r.EventInsertClient()

	seed := ORMCreateBatchInput{Model: "testmodule.item", Records: []map[string]any{
		{"id": "11111111-1111-1111-1111-111111111111", "tenant_id": "00000000-0000-0000-0000-000000000001", "name": "A", "code": "BATCH-A"},
		{"id": "22222222-2222-2222-2222-222222222222", "tenant_id": "00000000-0000-0000-0000-000000000001", "name": "B", "code": "BATCH-B"},
	}}
	if _, hostErr := ORMCreateBatch(ctx, r, primaryDB, insertClient, mc, seed); hostErr != nil {
		t.Fatalf("seed create_batch failed: %+v", hostErr)
	}

	// Each row supplies its own id (buildAssignment includes the primary
	// key column like any other assigned field — this isn't specific to
	// #936, and this test doesn't assert anything about "id" itself), so
	// only "name"/"number" distinguish the two update rows from each
	// other for the purpose of this assertion.
	upsert := ORMCreateBatchInput{
		Model: "testmodule.item",
		Records: []map[string]any{
			{"id": "33333333-3333-3333-3333-333333333333", "tenant_id": "00000000-0000-0000-0000-000000000001", "name": "C", "code": "BATCH-C"}, // new insert
			{"id": "11111111-1111-1111-1111-111111111111", "tenant_id": "00000000-0000-0000-0000-000000000001", "name": "A renamed", "code": "BATCH-A"},
			{"id": "22222222-2222-2222-2222-222222222222", "tenant_id": "00000000-0000-0000-0000-000000000001", "name": "B", "code": "BATCH-B", "number": int64(9)},
		},
		OnConflict: &OnConflictOption{Fields: []string{"code"}, Policy: "update"},
	}
	if _, hostErr := ORMCreateBatch(ctx, r, primaryDB, insertClient, mc, upsert); hostErr != nil {
		t.Fatalf("upsert create_batch failed: %+v", hostErr)
	}

	if got := countEventDeliveryJobsByName(t, primaryDB, "orm.record.created", slug); got != 2 {
		t.Errorf("orm.record.created jobs = %d, want 2 (one batched event for each create_batch call)", got)
	}

	events := updatedEventPayloads(t, primaryDB, slug)
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
