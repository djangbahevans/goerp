package wasm

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

// newConstraintTestModuleContext wires ComputedIndex/ComputeTargets the
// same way host_orm_compute_test.go's own fixtures do, but constraint
// hooks (unlike compute functions) don't need a ComputedIndex at all —
// runConstraintHook only ever borrows the calling module's own instance.
func newConstraintTestModuleContext(slug string, decls []model.ModelDeclaration, target ComputeTarget) *ModuleContext {
	return NewModuleContext("req-1", "testmodule", "user-1", "contact-1", []string{"admin"}, nil, slug, slug, "trace-1",
		abi.CapDBRead|abi.CapDBWrite, nil, ModuleSnapshot{
			ModelDecls:     decls,
			ComputeTargets: map[string]ComputeTarget{"testmodule": target},
		})
}

func TestORMCreate_ConstraintHook_UnregisteredPhase_Allowed(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("constraintcreate%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	createFixtureOrdersTable(t, primaryDB, slug)

	r := newComputeTestRuntime(t, primaryDB)
	decls := []model.ModelDeclaration{orderModelDecl()}
	target := newComputeTarget(t, ctx, r, decls)
	mc := newConstraintTestModuleContext(slug, decls, target)
	insertClient := r.EventInsertClient()

	// The fixture only registers a constraint hook for
	// ("testmodule.order", OnDelete) — create should proceed unaffected.
	out, hostErr := ORMCreate(ctx, r, primaryDB, insertClient, nil, mc, ORMCreateInput{
		Model:  "testmodule.order",
		Record: map[string]any{"id": "40000000-0000-0000-0000-000000000001", "tenant_id": "00000000-0000-0000-0000-000000000001", "state": "confirmed"},
	})
	if hostErr != nil {
		t.Fatalf("ORMCreate: %+v", hostErr)
	}
	if out.Record["state"] != "confirmed" {
		t.Errorf("state = %v, want confirmed", out.Record["state"])
	}
}

func TestORMUnlink_ConstraintHook_Rejects_NoRowDeleted(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("constraintreject%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	createFixtureOrdersTable(t, primaryDB, slug)

	r := newComputeTestRuntime(t, primaryDB)
	decls := []model.ModelDeclaration{orderModelDecl()}
	target := newComputeTarget(t, ctx, r, decls)
	mc := newConstraintTestModuleContext(slug, decls, target)
	insertClient := r.EventInsertClient()

	orderID := "40000000-0000-0000-0000-000000000002"
	if _, hostErr := ORMCreate(ctx, r, primaryDB, insertClient, nil, mc, ORMCreateInput{
		Model:  "testmodule.order",
		Record: map[string]any{"id": orderID, "tenant_id": "00000000-0000-0000-0000-000000000001", "state": "confirmed"},
	}); hostErr != nil {
		t.Fatalf("ORMCreate: %+v", hostErr)
	}

	_, hostErr := ORMUnlink(ctx, r, primaryDB, insertClient, nil, mc, ORMUnlinkInput{
		Model: "testmodule.order",
		IDs:   []string{orderID},
	})
	if hostErr == nil {
		t.Fatal("ORMUnlink: expected a rejection, got nil error")
	}
	if hostErr.Code != abi.ErrCodeValidationFailed {
		t.Errorf("hostErr.Code = %q, want %q", hostErr.Code, abi.ErrCodeValidationFailed)
	}
	if hostErr.Details["field"] != "state" {
		t.Errorf("hostErr.Details[field] = %v, want state", hostErr.Details["field"])
	}

	var count int
	if err := primaryDB.QueryRow(`SELECT COUNT(*) FROM tenant_` + slug + `.order WHERE id = '` + orderID + `'`).Scan(&count); err != nil {
		t.Fatalf("count order: %v", err)
	}
	if count != 1 {
		t.Errorf("order row count = %d, want 1 (delete must have been rejected, not just its event)", count)
	}
}

func TestORMUnlink_ConstraintHook_Allows_RowDeleted(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("constraintallow%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	createFixtureOrdersTable(t, primaryDB, slug)

	r := newComputeTestRuntime(t, primaryDB)
	decls := []model.ModelDeclaration{orderModelDecl()}
	target := newComputeTarget(t, ctx, r, decls)
	mc := newConstraintTestModuleContext(slug, decls, target)
	insertClient := r.EventInsertClient()

	orderID := "40000000-0000-0000-0000-000000000003"
	if _, hostErr := ORMCreate(ctx, r, primaryDB, insertClient, nil, mc, ORMCreateInput{
		Model:  "testmodule.order",
		Record: map[string]any{"id": orderID, "tenant_id": "00000000-0000-0000-0000-000000000001", "state": "draft"},
	}); hostErr != nil {
		t.Fatalf("ORMCreate: %+v", hostErr)
	}

	out, hostErr := ORMUnlink(ctx, r, primaryDB, insertClient, nil, mc, ORMUnlinkInput{
		Model: "testmodule.order",
		IDs:   []string{orderID},
	})
	if hostErr != nil {
		t.Fatalf("ORMUnlink: %+v", hostErr)
	}
	if out.Count != 1 || len(out.IDs) != 1 || out.IDs[0] != orderID {
		t.Errorf("ExecResult = %+v, want Count=1 IDs=[%s]", out, orderID)
	}

	var count int
	if err := primaryDB.QueryRow(`SELECT COUNT(*) FROM tenant_` + slug + `.order WHERE id = '` + orderID + `'`).Scan(&count); err != nil {
		t.Fatalf("count order: %v", err)
	}
	if count != 0 {
		t.Errorf("order row count = %d, want 0 (hard delete, no deleted_at field)", count)
	}
}

// TestORMWrite_ConstraintHook_NoLivePool_Allowed mirrors goerp#372's own
// widget-model dispatch coverage: a model whose module has no live pool
// at all still writes successfully — runConstraintHook must degrade
// gracefully rather than requiring a pool just to discover there's no
// hook to run.
func TestORMWrite_ConstraintHook_NoLivePool_Allowed(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("constraintnopool%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	createFixtureItemsTable(t, primaryDB, slug)

	r := newComputeTestRuntime(t, primaryDB)
	decls := []model.ModelDeclaration{itemModelDecl()}
	// No ComputeTargets entry at all for "testmodule" — the equivalent of
	// a module with no live pool.
	mc := NewModuleContext("req-1", "testmodule", "user-1", "contact-1", []string{"admin"}, nil, slug, slug, "trace-1",
		abi.CapDBRead|abi.CapDBWrite, nil, ModuleSnapshot{ModelDecls: decls})
	insertClient := r.EventInsertClient()

	itemID := "40000000-0000-0000-0000-000000000004"
	if _, hostErr := ORMCreate(ctx, r, primaryDB, insertClient, nil, mc, ORMCreateInput{
		Model:  "testmodule.item",
		Record: map[string]any{"id": itemID, "tenant_id": "00000000-0000-0000-0000-000000000001", "name": "Widget"},
	}); hostErr != nil {
		t.Fatalf("ORMCreate: %+v", hostErr)
	}

	out, hostErr := ORMWrite(ctx, r, primaryDB, insertClient, nil, mc, ORMWriteInput{
		Model:  "testmodule.item",
		ID:     itemID,
		Record: map[string]any{"name": "Renamed Widget"},
	})
	if hostErr != nil {
		t.Fatalf("ORMWrite: %+v", hostErr)
	}
	if out.Record["name"] != "Renamed Widget" {
		t.Errorf("name = %v, want Renamed Widget", out.Record["name"])
	}
}
