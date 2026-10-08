package wasm

import (
	"fmt"
	"testing"
	"time"
	"uuid"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

// Constraint hooks borrow the calling module directly and need no ComputedIndex. TenantID
// must be a UUID because creates fill the UUID tenant_id column from context.
func newConstraintTestModuleContext(slug string, decls []model.ModelDeclaration, target ComputeTarget) *ModuleContext {
	return NewModuleContext("req-1", "testmodule", "user-1", "contact-1", []string{"admin"}, nil, uuid.New().String(), slug, "trace-1",
		abi.CapDBRead|abi.CapDBWrite, nil, ModuleSnapshot{
			ModelDecls:     decls,
			ComputeTargets: map[string]ComputeTarget{"testmodule": target},
		})
}

func TestORMCreate_ConstraintHook_UnregisteredPhase_Allowed(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := t.Context()

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
	out, hostErr := ORMCreate(ctx, r, primaryDB, insertClient, nil, mc, abiv1.ORMCreateInput{
		Model:  "testmodule.order",
		Record: map[string]any{"state": "confirmed"},
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
	ctx := t.Context()

	slug := fmt.Sprintf("constraintreject%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	createFixtureOrdersTable(t, primaryDB, slug)

	r := newComputeTestRuntime(t, primaryDB)
	decls := []model.ModelDeclaration{orderModelDecl()}
	target := newComputeTarget(t, ctx, r, decls)
	mc := newConstraintTestModuleContext(slug, decls, target)
	insertClient := r.EventInsertClient()

	createOut, hostErr := ORMCreate(ctx, r, primaryDB, insertClient, nil, mc, abiv1.ORMCreateInput{
		Model:  "testmodule.order",
		Record: map[string]any{"state": "confirmed"},
	})
	if hostErr != nil {
		t.Fatalf("ORMCreate: %+v", hostErr)
	}
	orderID, _ := createOut.Record["id"].(string)

	_, hostErr = ORMUnlink(ctx, r, primaryDB, insertClient, nil, mc, abiv1.ORMUnlinkInput{
		Model: "testmodule.order",
		IDs:   []string{orderID},
	})
	if hostErr == nil {
		t.Fatal("ORMUnlink: expected a rejection, got nil error")
	}
	if hostErr.Code != abiv1.ErrCodeValidationFailed {
		t.Errorf("hostErr.Code = %q, want %q", hostErr.Code, abiv1.ErrCodeValidationFailed)
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
	ctx := t.Context()

	slug := fmt.Sprintf("constraintallow%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	createFixtureOrdersTable(t, primaryDB, slug)

	r := newComputeTestRuntime(t, primaryDB)
	decls := []model.ModelDeclaration{orderModelDecl()}
	target := newComputeTarget(t, ctx, r, decls)
	mc := newConstraintTestModuleContext(slug, decls, target)
	insertClient := r.EventInsertClient()

	createOut, hostErr := ORMCreate(ctx, r, primaryDB, insertClient, nil, mc, abiv1.ORMCreateInput{
		Model:  "testmodule.order",
		Record: map[string]any{"state": "draft"},
	})
	if hostErr != nil {
		t.Fatalf("ORMCreate: %+v", hostErr)
	}
	orderID, _ := createOut.Record["id"].(string)

	out, hostErr := ORMUnlink(ctx, r, primaryDB, insertClient, nil, mc, abiv1.ORMUnlinkInput{
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

func TestORMWrite_ConstraintHook_NoLivePool_Allowed(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := t.Context()

	slug := fmt.Sprintf("constraintnopool%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	createFixtureItemsTable(t, primaryDB, slug)

	r := newComputeTestRuntime(t, primaryDB)
	decls := []model.ModelDeclaration{itemModelDecl()}
	// No ComputeTargets entry at all for "testmodule" — the equivalent of
	// a module with no live pool.
	mc := NewModuleContext("req-1", "testmodule", "user-1", "contact-1", []string{"admin"}, nil, uuid.New().String(), slug, "trace-1",
		abi.CapDBRead|abi.CapDBWrite, nil, ModuleSnapshot{ModelDecls: decls})
	insertClient := r.EventInsertClient()

	createOut, hostErr := ORMCreate(ctx, r, primaryDB, insertClient, nil, mc, abiv1.ORMCreateInput{
		Model:  "testmodule.item",
		Record: map[string]any{"name": "Widget"},
	})
	if hostErr != nil {
		t.Fatalf("ORMCreate: %+v", hostErr)
	}
	itemID, _ := createOut.Record["id"].(string)

	out, hostErr := ORMWrite(ctx, r, primaryDB, insertClient, nil, mc, abiv1.ORMWriteInput{
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
