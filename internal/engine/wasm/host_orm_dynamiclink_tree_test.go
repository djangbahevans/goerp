package wasm

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

func categoryModelDecl() model.ModelDeclaration {
	return model.ModelDeclaration{
		Name: "category",
		Fields: []model.NamedField{
			{Name: "id", Def: model.UUID().Required().PrimaryKey()},
			{Name: "tenant_id", Def: model.UUID().Required()},
			{Name: "parent_id", Def: model.Many2One("testmodule.category").Tree()},
			{Name: "name", Def: model.Text()},
		},
	}
}

func createFixtureCategoryTable(t *testing.T, conn *sql.DB, slug string) {
	t.Helper()
	ctx := context.Background()
	schemaName := "tenant_" + slug

	if _, err := conn.ExecContext(ctx, `CREATE TABLE `+schemaName+`.category (
		id UUID PRIMARY KEY,
		tenant_id UUID NOT NULL,
		parent_id UUID REFERENCES `+schemaName+`.category(id),
		parent_id_path ltree,
		name TEXT
	)`); err != nil {
		t.Fatalf("create category table: %v", err)
	}
}

func newTreeTestModuleContext(slug string, decls []model.ModelDeclaration) *ModuleContext {
	return NewModuleContext("req-1", "testmodule", "user-1", "contact-1", []string{"admin"}, slug, slug, "trace-1",
		abi.CapDBRead|abi.CapDBWrite, nil, ModuleSnapshot{ModelDecls: decls})
}

func categoryPath(t *testing.T, conn *sql.DB, slug, id string) string {
	t.Helper()
	var path sql.NullString
	if err := conn.QueryRow(`SELECT parent_id_path::text FROM tenant_`+slug+`.category WHERE id = $1`, id).Scan(&path); err != nil {
		t.Fatalf("query path for %s: %v", id, err)
	}
	return path.String
}

func TestORMCreate_Tree_RootGetsSingleLabelPath(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("treeroot%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	createFixtureCategoryTable(t, primaryDB, slug)

	r := newComputeTestRuntime(t, primaryDB)
	decls := []model.ModelDeclaration{categoryModelDecl()}
	mc := newTreeTestModuleContext(slug, decls)
	insertClient := r.EventInsertClient()

	rootID := "50000000-0000-0000-0000-000000000001"
	if _, hostErr := ORMCreate(ctx, r, primaryDB, insertClient, nil, mc, ORMCreateInput{
		Model:  "testmodule.category",
		Record: map[string]any{"id": rootID, "tenant_id": "00000000-0000-0000-0000-000000000001", "name": "Root"},
	}); hostErr != nil {
		t.Fatalf("ORMCreate: %+v", hostErr)
	}

	want := ltreeLabel(rootID)
	if got := categoryPath(t, primaryDB, slug, rootID); got != want {
		t.Errorf("root path = %q, want %q", got, want)
	}
}

func TestORMCreate_Tree_ChildGetsParentPathPlusOwnLabel(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("treechild%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	createFixtureCategoryTable(t, primaryDB, slug)

	r := newComputeTestRuntime(t, primaryDB)
	decls := []model.ModelDeclaration{categoryModelDecl()}
	mc := newTreeTestModuleContext(slug, decls)
	insertClient := r.EventInsertClient()

	rootID := "50000000-0000-0000-0000-000000000002"
	childID := "50000000-0000-0000-0000-000000000003"
	if _, hostErr := ORMCreate(ctx, r, primaryDB, insertClient, nil, mc, ORMCreateInput{
		Model:  "testmodule.category",
		Record: map[string]any{"id": rootID, "tenant_id": "00000000-0000-0000-0000-000000000001", "name": "Root"},
	}); hostErr != nil {
		t.Fatalf("create root: %+v", hostErr)
	}
	if _, hostErr := ORMCreate(ctx, r, primaryDB, insertClient, nil, mc, ORMCreateInput{
		Model:  "testmodule.category",
		Record: map[string]any{"id": childID, "tenant_id": "00000000-0000-0000-0000-000000000001", "name": "Child", "parent_id": rootID},
	}); hostErr != nil {
		t.Fatalf("create child: %+v", hostErr)
	}

	want := ltreeLabel(rootID) + "." + ltreeLabel(childID)
	if got := categoryPath(t, primaryDB, slug, childID); got != want {
		t.Errorf("child path = %q, want %q", got, want)
	}
}

func TestORMWrite_Tree_ReparentUpdatesWholeSubtree(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("treereparent%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	createFixtureCategoryTable(t, primaryDB, slug)

	r := newComputeTestRuntime(t, primaryDB)
	decls := []model.ModelDeclaration{categoryModelDecl()}
	mc := newTreeTestModuleContext(slug, decls)
	insertClient := r.EventInsertClient()

	aID := "50000000-0000-0000-0000-000000000004"
	bID := "50000000-0000-0000-0000-000000000005"
	cID := "50000000-0000-0000-0000-000000000006"
	tenantID := "00000000-0000-0000-0000-000000000001"

	for _, rec := range []map[string]any{
		{"id": aID, "tenant_id": tenantID, "name": "A"},
		{"id": bID, "tenant_id": tenantID, "name": "B", "parent_id": aID},
		{"id": cID, "tenant_id": tenantID, "name": "C", "parent_id": bID},
	} {
		if _, hostErr := ORMCreate(ctx, r, primaryDB, insertClient, nil, mc, ORMCreateInput{Model: "testmodule.category", Record: rec}); hostErr != nil {
			t.Fatalf("create %v: %+v", rec["id"], hostErr)
		}
	}

	// Reparent B (and its descendant C) to root — parent_id: nil.
	if _, hostErr := ORMWrite(ctx, r, primaryDB, insertClient, nil, mc, ORMWriteInput{
		Model:  "testmodule.category",
		ID:     bID,
		Record: map[string]any{"parent_id": nil},
	}); hostErr != nil {
		t.Fatalf("ORMWrite reparent: %+v", hostErr)
	}

	if got, want := categoryPath(t, primaryDB, slug, bID), ltreeLabel(bID); got != want {
		t.Errorf("B path = %q, want %q", got, want)
	}
	if got, want := categoryPath(t, primaryDB, slug, cID), ltreeLabel(bID)+"."+ltreeLabel(cID); got != want {
		t.Errorf("C path = %q, want %q (descendant should move with B)", got, want)
	}
	if got, want := categoryPath(t, primaryDB, slug, aID), ltreeLabel(aID); got != want {
		t.Errorf("A path = %q, want %q (unrelated row must not change)", got, want)
	}
}

func TestORMWrite_Tree_CycleDetected(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("treecycle%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	createFixtureCategoryTable(t, primaryDB, slug)

	r := newComputeTestRuntime(t, primaryDB)
	decls := []model.ModelDeclaration{categoryModelDecl()}
	mc := newTreeTestModuleContext(slug, decls)
	insertClient := r.EventInsertClient()

	aID := "50000000-0000-0000-0000-000000000007"
	bID := "50000000-0000-0000-0000-000000000008"
	tenantID := "00000000-0000-0000-0000-000000000001"

	for _, rec := range []map[string]any{
		{"id": aID, "tenant_id": tenantID, "name": "A"},
		{"id": bID, "tenant_id": tenantID, "name": "B", "parent_id": aID},
	} {
		if _, hostErr := ORMCreate(ctx, r, primaryDB, insertClient, nil, mc, ORMCreateInput{Model: "testmodule.category", Record: rec}); hostErr != nil {
			t.Fatalf("create %v: %+v", rec["id"], hostErr)
		}
	}

	aPathBefore := categoryPath(t, primaryDB, slug, aID)

	// Reparent A (the ancestor) under B (its own descendant) — a cycle.
	_, hostErr := ORMWrite(ctx, r, primaryDB, insertClient, nil, mc, ORMWriteInput{
		Model:  "testmodule.category",
		ID:     aID,
		Record: map[string]any{"parent_id": bID},
	})
	if hostErr == nil {
		t.Fatal("expected orm.cycle_detected, got nil error")
	}
	if hostErr.Code != abi.ErrCodeCycleDetected {
		t.Errorf("hostErr.Code = %q, want %q", hostErr.Code, abi.ErrCodeCycleDetected)
	}

	if got := categoryPath(t, primaryDB, slug, aID); got != aPathBefore {
		t.Errorf("A path = %q after rejected reparent, want unchanged %q", got, aPathBefore)
	}
}

// commentModelDecl and orderTargetModelDecl exercise DynamicLink's
// cross-module target resolution: comment.reference_id can point at a
// model owned by a *different* module than the one calling host.orm.
func commentModelDecl() model.ModelDeclaration {
	return model.ModelDeclaration{
		Name: "comment",
		Fields: []model.NamedField{
			{Name: "id", Def: model.UUID().Required().PrimaryKey()},
			{Name: "tenant_id", Def: model.UUID().Required()},
			{Name: "reference_type", Def: model.Selection("salesmod.target_order")},
			{Name: "reference_id", Def: model.DynamicLink("reference_type")},
		},
	}
}

func orderTargetModelDecl() model.ModelDeclaration {
	return model.ModelDeclaration{
		Name:   "target_order",
		Fields: []model.NamedField{{Name: "id", Def: model.UUID().Required().PrimaryKey()}},
	}
}

func createFixtureCommentAndTargetOrderTables(t *testing.T, conn *sql.DB, slug string) {
	t.Helper()
	ctx := context.Background()
	schemaName := "tenant_" + slug

	if _, err := conn.ExecContext(ctx, `CREATE TABLE `+schemaName+`.comment (
		id UUID PRIMARY KEY,
		tenant_id UUID NOT NULL,
		reference_type TEXT,
		reference_id UUID
	)`); err != nil {
		t.Fatalf("create comment table: %v", err)
	}
	if _, err := conn.ExecContext(ctx, `CREATE TABLE `+schemaName+`.target_order (id UUID PRIMARY KEY)`); err != nil {
		t.Fatalf("create target_order table: %v", err)
	}
}

func TestORMCreate_DynamicLink_MissingPairField_Rejected(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("dlpair%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	createFixtureCommentAndTargetOrderTables(t, primaryDB, slug)

	r := newComputeTestRuntime(t, primaryDB)
	decls := []model.ModelDeclaration{commentModelDecl()}
	mc := newTreeTestModuleContext(slug, decls)
	insertClient := r.EventInsertClient()

	_, hostErr := ORMCreate(ctx, r, primaryDB, insertClient, nil, mc, ORMCreateInput{
		Model: "testmodule.comment",
		Record: map[string]any{
			"id": "60000000-0000-0000-0000-000000000001", "tenant_id": "00000000-0000-0000-0000-000000000001",
			"reference_id": "60000000-0000-0000-0000-000000000002", // reference_type missing
		},
	})
	if hostErr == nil || hostErr.Code != abi.ErrCodeValidationFailed {
		t.Fatalf("hostErr = %+v, want code %s", hostErr, abi.ErrCodeValidationFailed)
	}
}

func TestORMCreate_DynamicLink_NonexistentTarget_Rejected(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("dlmissing%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	createFixtureCommentAndTargetOrderTables(t, primaryDB, slug)

	r := newComputeTestRuntime(t, primaryDB)
	decls := []model.ModelDeclaration{commentModelDecl()}
	mc := NewModuleContext("req-1", "testmodule", "user-1", "contact-1", []string{"admin"}, slug, slug, "trace-1",
		abi.CapDBRead|abi.CapDBWrite, nil, ModuleSnapshot{
			ModelDecls:     decls,
			ComputeTargets: map[string]ComputeTarget{"salesmod": {ModelDecls: []model.ModelDeclaration{orderTargetModelDecl()}}},
		})
	insertClient := r.EventInsertClient()

	_, hostErr := ORMCreate(ctx, r, primaryDB, insertClient, nil, mc, ORMCreateInput{
		Model: "testmodule.comment",
		Record: map[string]any{
			"id": "60000000-0000-0000-0000-000000000003", "tenant_id": "00000000-0000-0000-0000-000000000001",
			"reference_type": "salesmod.target_order",
			"reference_id":   "60000000-0000-0000-0000-000000000099", // never created
		},
	})
	if hostErr == nil || hostErr.Code != abi.ErrCodeDynamicLinkTargetNotFound {
		t.Fatalf("hostErr = %+v, want code %s", hostErr, abi.ErrCodeDynamicLinkTargetNotFound)
	}
}

func TestORMCreate_DynamicLink_ValidCrossModuleTarget_Succeeds(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("dlvalid%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	createFixtureCommentAndTargetOrderTables(t, primaryDB, slug)

	targetOrderID := "60000000-0000-0000-0000-000000000004"
	if _, err := primaryDB.Exec(`INSERT INTO tenant_`+slug+`.target_order (id) VALUES ($1)`, targetOrderID); err != nil {
		t.Fatalf("seed target_order: %v", err)
	}

	r := newComputeTestRuntime(t, primaryDB)
	decls := []model.ModelDeclaration{commentModelDecl()}
	mc := NewModuleContext("req-1", "testmodule", "user-1", "contact-1", []string{"admin"}, slug, slug, "trace-1",
		abi.CapDBRead|abi.CapDBWrite, nil, ModuleSnapshot{
			ModelDecls:     decls,
			ComputeTargets: map[string]ComputeTarget{"salesmod": {ModelDecls: []model.ModelDeclaration{orderTargetModelDecl()}}},
		})
	insertClient := r.EventInsertClient()

	out, hostErr := ORMCreate(ctx, r, primaryDB, insertClient, nil, mc, ORMCreateInput{
		Model: "testmodule.comment",
		Record: map[string]any{
			"id": "60000000-0000-0000-0000-000000000005", "tenant_id": "00000000-0000-0000-0000-000000000001",
			"reference_type": "salesmod.target_order",
			"reference_id":   targetOrderID,
		},
	})
	if hostErr != nil {
		t.Fatalf("ORMCreate: %+v", hostErr)
	}
	if out.Record["reference_id"] != targetOrderID {
		t.Errorf("reference_id = %v, want %v", out.Record["reference_id"], targetOrderID)
	}
}
