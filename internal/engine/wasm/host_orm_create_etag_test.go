package wasm

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

type ormCreateEtagFixture struct {
	ctx       context.Context
	primaryDB *sql.DB
	r         *Runtime
	mc        *ModuleContext
}

func newORMCreateEtagFixture(t *testing.T, name string) ormCreateEtagFixture {
	t.Helper()
	primaryDB := openTestPrimaryDB(t)
	slug := fmt.Sprintf("%s%d", name, time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	createFixtureItemsTable(t, primaryDB, slug)
	mc, _ := newORMCreateChangedFieldsModuleContext(slug, []model.ModelDeclaration{itemModelDecl()})
	return ormCreateEtagFixture{ctx: context.Background(), primaryDB: primaryDB, r: newHostDBTestRuntime(t, primaryDB, 10), mc: mc}
}

func (f ormCreateEtagFixture) create(input abiv1.ORMCreateInput) (map[string]any, *abiv1.HostError) {
	out, hostErr := ORMCreate(f.ctx, f.r, f.primaryDB, f.r.EventInsertClient(), nil, f.mc, input)
	return out.Record, hostErr
}

func TestHostORM_CreateBatch_GivesEachRecordItsOwnEtag(t *testing.T) {
	f := newORMCreateEtagFixture(t, "ormbatchetag")

	out, hostErr := ORMCreateBatch(f.ctx, f.r, f.primaryDB, f.r.EventInsertClient(), f.mc, abiv1.ORMCreateBatchInput{
		Model:   "testmodule.item",
		Records: []map[string]any{{"name": "A"}, {"name": "B"}},
	})
	if hostErr != nil {
		t.Fatalf("create batch failed: %+v", hostErr)
	}
	a, _ := out.Records[0]["etag"].(string)
	b, _ := out.Records[1]["etag"].(string)
	if a == "" || b == "" || a == b {
		t.Errorf("etags = %q, %q, want two different non-empty etags", a, b)
	}
}

func TestHostORM_FirstOrCreate_CreatedRecordHasEtag(t *testing.T) {
	f := newORMCreateEtagFixture(t, "ormfirstorcreateetag")

	out, hostErr := ORMFirstOrCreate(f.ctx, f.r, f.primaryDB, f.r.EventInsertClient(), f.mc, abiv1.ORMFirstOrCreateInput{
		Model:      "testmodule.item",
		UniqueVals: map[string]any{"code": "FOC1"},
		CreateVals: map[string]any{"name": "A"},
	})
	if hostErr != nil {
		t.Fatalf("first or create failed: %+v", hostErr)
	}
	if !out.Created {
		t.Fatal("Created = false, want a new record")
	}
	if etag, _ := out.Record["etag"].(string); etag == "" {
		t.Error("Record[etag] is empty, want an engine-generated etag")
	}
}

func TestHostORM_Create_OnConflictUpdate_RotatesEtagOfTheChangedRow(t *testing.T) {
	f := newORMCreateEtagFixture(t, "ormupsertetag")

	first, hostErr := f.create(abiv1.ORMCreateInput{Model: "testmodule.item", Record: map[string]any{"name": "A", "code": "UP1"}})
	if hostErr != nil {
		t.Fatalf("first create failed: %+v", hostErr)
	}
	updated, hostErr := f.create(abiv1.ORMCreateInput{
		Model:      "testmodule.item",
		Record:     map[string]any{"name": "A updated", "code": "UP1"},
		OnConflict: &abiv1.ORMOnConflict{Fields: []string{"code"}, Policy: "update"},
	})
	if hostErr != nil {
		t.Fatalf("upsert failed: %+v", hostErr)
	}
	if updated["id"] != first["id"] {
		t.Fatalf("upsert id = %v, want the existing row %v", updated["id"], first["id"])
	}
	before, _ := first["etag"].(string)
	after, _ := updated["etag"].(string)
	if after == "" || after == before {
		t.Errorf("etag after upsert = %q, want a new etag, not %q", after, before)
	}
}

func TestHostORM_Create_RejectsACallerSuppliedEtag(t *testing.T) {
	f := newORMCreateEtagFixture(t, "ormsuppliedetag")

	_, hostErr := f.create(abiv1.ORMCreateInput{Model: "testmodule.item", Record: map[string]any{"name": "A", "etag": "mine"}})
	if hostErr == nil || hostErr.Code != abiv1.ErrCodeFieldNotWritable {
		t.Errorf("create with an etag = %+v, want %s", hostErr, abiv1.ErrCodeFieldNotWritable)
	}
}
