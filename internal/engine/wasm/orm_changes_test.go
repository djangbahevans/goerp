package wasm

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"testing"
	"time"
	"uuid"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

type recordChangeFunc func(context.Context, string, string, string, string)

func (f recordChangeFunc) RecordChanged(ctx context.Context, tenantID, resource, recordID, operation string) {
	f(ctx, tenantID, resource, recordID, operation)
}

type ormChangesFixture struct {
	db      *sql.DB
	runtime *Runtime
	mc      *ModuleContext
	changes []ormChange
}

func newORMChangesFixture(t *testing.T) *ormChangesFixture {
	t.Helper()

	primary := openTestPrimaryDB(t)
	slug := fmt.Sprintf("ormchanges%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primary, slug)
	createFixtureWidgetsTable(t, primary, slug, nil)
	grantFixtureTables(t, primary, slug, "widgets")
	rt := newHostDBTestRuntime(t, primary, 10)
	mc, _ := newORMWriteTestModuleContext(slug, []model.ModelDeclaration{widgetModelDecl()})
	mc.txLimiter = rt.TxLimiter()
	t.Cleanup(mc.RollbackAll)
	f := &ormChangesFixture{db: primary, runtime: rt, mc: mc}
	rt.SetRecordChangePublisher(recordChangeFunc(func(ctx context.Context, tenantID, resource, recordID, operation string) {
		if tenantID != mc.TenantID {
			t.Errorf("change tenant = %q, want %q", tenantID, mc.TenantID)
		}
		if got := countEventDeliveryJobs(t, primary, tenantID); got == 0 {
			t.Error("live change published before its event transaction committed")
		}
		f.changes = append(f.changes, ormChange{resource: resource, recordID: recordID, operation: operation})
	}))
	return f
}

func (f *ormChangesFixture) expect(t *testing.T, want ...ormChange) {
	t.Helper()
	if !slices.Equal(f.changes, want) {
		t.Fatalf("changes = %+v, want %+v", f.changes, want)
	}
	f.changes = nil
	if len(f.mc.ormChanges) != 0 {
		t.Fatal("finished transaction retained live changes")
	}
}

func TestHostORM_LiveChanges_AllWriteVariants(t *testing.T) {
	f := newORMChangesFixture(t)
	ctx := t.Context()
	inst := newHostORMWriteCaller(t, ctx, f.runtime, f.mc)
	mutate := newHostCallerInstance(t, ctx, f.runtime, buildHostCallerModule("host.orm", []string{"mutate"}), f.mc)
	const resource = "testmodule.widget"
	id1, id2 := uuid.New().String(), uuid.New().String()
	call := func(inst *ModuleInstance, method string, input any) {
		t.Helper()
		if env := callHost(t, ctx, inst, "call_"+method, input); !env.OK {
			t.Fatalf("%s: %+v", method, env.Error)
		}
	}
	changed := func(id, operation string) ormChange {
		return ormChange{resource: resource, recordID: id, operation: operation}
	}
	call(inst, "create", abiv1.ORMCreateInput{
		Model:  resource,
		Record: map[string]any{"id": id1, "name": "first", "price": 10},
	})
	f.expect(t, changed("", "created"))

	call(inst, "create_batch", abiv1.ORMCreateBatchInput{
		Model:   resource,
		Records: []map[string]any{{"id": id2, "name": "second", "price": 20}},
	})
	f.expect(t, changed("", "created"))

	call(inst, "write", abiv1.ORMWriteInput{
		Model:  resource,
		ID:     id1,
		Record: map[string]any{"name": "updated"},
	})
	f.expect(t, changed(id1, "updated"))

	call(inst, "write_many", abiv1.ORMWriteManyInput{
		Model:  resource,
		IDs:    []string{id1, id2},
		Record: map[string]any{"price": 30},
	})
	f.expect(t, changed(id1, "updated"), changed(id2, "updated"))

	call(inst, "write_where", abiv1.ORMWriteWhereInput{
		Model:  resource,
		Domain: "record.id = '" + id1 + "'",
		Record: map[string]any{"price": 40},
	})
	f.expect(t, changed(id1, "updated"))

	call(mutate, "mutate", abiv1.ORMMutateInput{
		Model: resource,
		ID:    id1,
		Ops:   []abiv1.ORMMutateOp{{Field: "price", Delta: int64(1)}},
	})
	f.expect(t, changed(id1, "updated"))

	call(inst, "first_or_create", abiv1.ORMFirstOrCreateInput{
		Model:      resource,
		UniqueVals: map[string]any{"id": id1},
		CreateVals: map[string]any{"name": "ignored"},
	})
	f.expect(t)

	id3 := uuid.New().String()
	call(inst, "first_or_create", abiv1.ORMFirstOrCreateInput{
		Model:      resource,
		UniqueVals: map[string]any{"id": id3},
		CreateVals: map[string]any{"name": "third"},
	})
	f.expect(t, changed("", "created"))

	conflict := &abiv1.ORMOnConflict{Fields: []string{"id"}, Policy: "update"}
	call(inst, "create", abiv1.ORMCreateInput{
		Model:      resource,
		Record:     map[string]any{"id": id1, "name": "upsert"},
		OnConflict: conflict,
	})
	f.expect(t, changed(id1, "updated"))

	conflict.Policy = "ignore"
	call(inst, "create", abiv1.ORMCreateInput{
		Model:      resource,
		Record:     map[string]any{"id": id1, "name": "ignored"},
		OnConflict: conflict,
	})
	f.expect(t)

	call(inst, "write_where", abiv1.ORMWriteWhereInput{
		Model:  resource,
		Domain: "record.name = 'missing'",
		Record: map[string]any{"price": 99},
	})
	f.expect(t)

	call(inst, "unlink", abiv1.ORMUnlinkInput{
		Model: resource,
		IDs:   []string{id1, id2, id3},
	})
	f.expect(t, changed(id1, "deleted"), changed(id2, "deleted"), changed(id3, "deleted"))
}

func TestHostORM_LiveChanges_TransactionOutcomes(t *testing.T) {
	for _, outcome := range []string{"commit", "rollback", "abandoned", "managed_commit", "managed_rollback", "commit_failed", "managed_commit_failed"} {
		t.Run(outcome, func(t *testing.T) {
			f := newORMChangesFixture(t)
			ctx := t.Context()
			dbCaller := newHostDBCaller(t, ctx, f.runtime, f.mc)
			var txID string
			var managed *ManagedTransaction
			switch outcome {
			case "managed_commit", "managed_rollback", "managed_commit_failed":
				var err error
				managed, err = f.runtime.BeginManagedTransaction(ctx, f.mc)
				if err != nil {
					t.Fatal(err)
				}
				defer managed.Rollback()
				txID = managed.ID
			default:
				var out abiv1.DBBeginOutput
				if env := callORMHost(t, ctx, dbCaller, "call_begin", abiv1.DBBeginInput{}, &out); !env.OK {
					t.Fatal(env.Error)
				}
				txID = out.TxID
			}
			id := uuid.New().String()
			input := abiv1.ORMCreateInput{Model: "testmodule.widget", TxID: txID, Record: map[string]any{"id": id, "name": "new"}}
			if _, hostErr := ORMCreate(ctx, f.runtime, f.db, f.runtime.EventInsertClient(), nil, f.mc, input); hostErr != nil {
				t.Fatal(hostErr)
			}
			if _, hostErr := ORMWrite(ctx, f.runtime, f.db, f.runtime.EventInsertClient(), nil, f.mc, abiv1.ORMWriteInput{Model: input.Model, TxID: txID, ID: id, Record: map[string]any{"name": "updated"}}); hostErr != nil {
				t.Fatal(hostErr)
			}
			if _, hostErr := ORMUnlink(ctx, f.runtime, f.db, f.runtime.EventInsertClient(), nil, f.mc, abiv1.ORMUnlinkInput{
				Model: input.Model,
				TxID:  txID,
				IDs:   []string{id},
			}); hostErr != nil {
				t.Fatal(hostErr)
			}
			if len(f.changes) != 0 {
				t.Fatal("uncommitted write published a live change")
			}
			switch outcome {
			case "commit_failed", "managed_commit_failed":
				tx, _ := f.mc.Transaction(txID)
				if _, err := tx.ExecContext(ctx, "SELECT 1/0"); err == nil {
					t.Fatal("expected transaction failure")
				}
				if managed != nil {
					if err := managed.Commit(); err == nil {
						t.Fatal("expected commit failure")
					}
				} else if env := callHost(t, ctx, dbCaller, "call_commit", abiv1.DBTxIDInput{TxID: txID}); env.OK {
					t.Fatal("expected commit failure")
				}
			case "commit", "rollback":
				if env := callHost(t, ctx, dbCaller, "call_"+outcome, abiv1.DBTxIDInput{TxID: txID}); !env.OK {
					t.Fatal(env.Error)
				}
			case "managed_commit":
				if err := managed.Commit(); err != nil {
					t.Fatal(err)
				}
			case "managed_rollback":
				managed.Rollback()
			case "abandoned":
				f.mc.RollbackAll()
			}
			if outcome == "commit" || outcome == "managed_commit" {
				f.expect(t,
					ormChange{resource: input.Model, operation: "created"},
					ormChange{resource: input.Model, recordID: id, operation: "updated"},
					ormChange{resource: input.Model, recordID: id, operation: "deleted"},
				)
			} else {
				f.expect(t)
			}
		})
	}
}

func TestHostORM_LiveChanges_OwnedCommitFailureAndRawSQL(t *testing.T) {
	f := newORMChangesFixture(t)
	ctx := t.Context()
	if _, err := f.db.ExecContext(ctx, "ALTER TABLE tenant_"+f.mc.TenantSlug+".widgets ADD UNIQUE (name) DEFERRABLE INITIALLY DEFERRED"); err != nil {
		t.Fatal(err)
	}
	for i := range 2 {
		_, hostErr := ORMCreate(ctx, f.runtime, f.db, f.runtime.EventInsertClient(), nil, f.mc, abiv1.ORMCreateInput{Model: "testmodule.widget", Record: map[string]any{"id": uuid.New().String(), "name": "duplicate"}})
		if i == 0 {
			if hostErr != nil {
				t.Fatal(hostErr)
			}
			f.expect(t, ormChange{resource: "testmodule.widget", operation: "created"})
		} else {
			if hostErr == nil || hostErr.Code != abiv1.ErrCodeCommitFailed {
				t.Fatalf("expected deferred commit failure, got %v", hostErr)
			}
			f.expect(t)
		}
	}
	inst := newHostCallerInstance(t, ctx, f.runtime, buildHostCallerModule("host.db", []string{"exec"}), f.mc)
	if env := callHost(t, ctx, inst, "call_exec", abiv1.DBExecInput{SQL: "UPDATE widgets SET name = 'raw'"}); !env.OK {
		t.Fatal(env.Error)
	}
	f.expect(t)

}
