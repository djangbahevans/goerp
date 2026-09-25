package wasm

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"
	"uuid"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

func categoryModelDecl() model.ModelDeclaration {
	return model.ModelDeclaration{
		Name: "category",
		Fields: []model.NamedField{
			{Name: "id", Def: model.UUID().Required().PrimaryKey().Default("uuidv7()")},
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
		id UUID PRIMARY KEY DEFAULT uuidv7(),
		tenant_id UUID NOT NULL,
		parent_id UUID REFERENCES `+schemaName+`.category(id),
		parent_id_path ltree,
		name TEXT
	)`); err != nil {
		t.Fatalf("create category table: %v", err)
	}
}

// TenantID is a fresh UUID, not slug (goerp#992: tenant_id is Readonly,
// so a create omitting it gets it auto-filled straight from
// ModuleContext.TenantID — the non-UUID slug can't go into that column).
func newTreeTestModuleContext(slug string, decls []model.ModelDeclaration) *ModuleContext {
	return NewModuleContext("req-1", "testmodule", "user-1", "contact-1", []string{"admin"}, nil, uuid.New().String(), slug, "trace-1",
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

	created, hostErr := ORMCreate(ctx, r, primaryDB, insertClient, nil, mc, abiv1.ORMCreateInput{
		Model:  "testmodule.category",
		Record: map[string]any{"name": "Root"},
	})
	if hostErr != nil {
		t.Fatalf("ORMCreate: %+v", hostErr)
	}
	rootID, _ := created.Record["id"].(string)

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

	rootOut, hostErr := ORMCreate(ctx, r, primaryDB, insertClient, nil, mc, abiv1.ORMCreateInput{
		Model:  "testmodule.category",
		Record: map[string]any{"name": "Root"},
	})
	if hostErr != nil {
		t.Fatalf("create root: %+v", hostErr)
	}
	rootID, _ := rootOut.Record["id"].(string)

	childOut, hostErr := ORMCreate(ctx, r, primaryDB, insertClient, nil, mc, abiv1.ORMCreateInput{
		Model:  "testmodule.category",
		Record: map[string]any{"name": "Child", "parent_id": rootID},
	})
	if hostErr != nil {
		t.Fatalf("create child: %+v", hostErr)
	}
	childID, _ := childOut.Record["id"].(string)

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

	aOut, hostErr := ORMCreate(ctx, r, primaryDB, insertClient, nil, mc, abiv1.ORMCreateInput{Model: "testmodule.category", Record: map[string]any{"name": "A"}})
	if hostErr != nil {
		t.Fatalf("create A: %+v", hostErr)
	}
	aID, _ := aOut.Record["id"].(string)

	bOut, hostErr := ORMCreate(ctx, r, primaryDB, insertClient, nil, mc, abiv1.ORMCreateInput{Model: "testmodule.category", Record: map[string]any{"name": "B", "parent_id": aID}})
	if hostErr != nil {
		t.Fatalf("create B: %+v", hostErr)
	}
	bID, _ := bOut.Record["id"].(string)

	cOut, hostErr := ORMCreate(ctx, r, primaryDB, insertClient, nil, mc, abiv1.ORMCreateInput{Model: "testmodule.category", Record: map[string]any{"name": "C", "parent_id": bID}})
	if hostErr != nil {
		t.Fatalf("create C: %+v", hostErr)
	}
	cID, _ := cOut.Record["id"].(string)

	// Reparent B (and its descendant C) to root — parent_id: nil.
	if _, hostErr := ORMWrite(ctx, r, primaryDB, insertClient, nil, mc, abiv1.ORMWriteInput{
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

	aOut, hostErr := ORMCreate(ctx, r, primaryDB, insertClient, nil, mc, abiv1.ORMCreateInput{Model: "testmodule.category", Record: map[string]any{"name": "A"}})
	if hostErr != nil {
		t.Fatalf("create A: %+v", hostErr)
	}
	aID, _ := aOut.Record["id"].(string)

	bOut, hostErr := ORMCreate(ctx, r, primaryDB, insertClient, nil, mc, abiv1.ORMCreateInput{Model: "testmodule.category", Record: map[string]any{"name": "B", "parent_id": aID}})
	if hostErr != nil {
		t.Fatalf("create B: %+v", hostErr)
	}
	bID, _ := bOut.Record["id"].(string)

	aPathBefore := categoryPath(t, primaryDB, slug, aID)

	// Reparent A (the ancestor) under B (its own descendant) — a cycle.
	_, hostErr = ORMWrite(ctx, r, primaryDB, insertClient, nil, mc, abiv1.ORMWriteInput{
		Model:  "testmodule.category",
		ID:     aID,
		Record: map[string]any{"parent_id": bID},
	})
	if hostErr == nil {
		t.Fatal("expected orm.cycle_detected, got nil error")
	}
	if hostErr.Code != abiv1.ErrCodeCycleDetected {
		t.Errorf("hostErr.Code = %q, want %q", hostErr.Code, abiv1.ErrCodeCycleDetected)
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
			{Name: "id", Def: model.UUID().Required().PrimaryKey().Default("uuidv7()")},
			{Name: "tenant_id", Def: model.UUID().Required()},
			{Name: "reference_type", Def: model.Selection("salesmod.target_order")},
			{Name: "reference_id", Def: model.DynamicLink("reference_type")},
		},
	}
}

func orderTargetModelDecl() model.ModelDeclaration {
	return model.ModelDeclaration{
		Name:   "target_order",
		Fields: []model.NamedField{{Name: "id", Def: model.UUID().Required().PrimaryKey().Default("uuidv7()")}},
	}
}

func createFixtureCommentAndTargetOrderTables(t *testing.T, conn *sql.DB, slug string) {
	t.Helper()
	ctx := context.Background()
	schemaName := "tenant_" + slug

	if _, err := conn.ExecContext(ctx, `CREATE TABLE `+schemaName+`.comment (
		id UUID PRIMARY KEY DEFAULT uuidv7(),
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

	_, hostErr := ORMCreate(ctx, r, primaryDB, insertClient, nil, mc, abiv1.ORMCreateInput{
		Model: "testmodule.comment",
		Record: map[string]any{
			"reference_id": "60000000-0000-0000-0000-000000000002", // reference_type missing
		},
	})
	if hostErr == nil || hostErr.Code != abiv1.ErrCodeValidationFailed {
		t.Fatalf("hostErr = %+v, want code %s", hostErr, abiv1.ErrCodeValidationFailed)
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
	mc := NewModuleContext("req-1", "testmodule", "user-1", "contact-1", []string{"admin"}, nil, uuid.New().String(), slug, "trace-1",
		abi.CapDBRead|abi.CapDBWrite, nil, ModuleSnapshot{
			ModelDecls:     decls,
			ComputeTargets: map[string]ComputeTarget{"salesmod": {ModelDecls: []model.ModelDeclaration{orderTargetModelDecl()}}},
		})
	insertClient := r.EventInsertClient()

	_, hostErr := ORMCreate(ctx, r, primaryDB, insertClient, nil, mc, abiv1.ORMCreateInput{
		Model: "testmodule.comment",
		Record: map[string]any{
			"reference_type": "salesmod.target_order",
			"reference_id":   "60000000-0000-0000-0000-000000000099", // never created
		},
	})
	if hostErr == nil || hostErr.Code != abiv1.ErrCodeDynamicLinkTargetNotFound {
		t.Fatalf("hostErr = %+v, want code %s", hostErr, abiv1.ErrCodeDynamicLinkTargetNotFound)
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
	mc := NewModuleContext("req-1", "testmodule", "user-1", "contact-1", []string{"admin"}, nil, uuid.New().String(), slug, "trace-1",
		abi.CapDBRead|abi.CapDBWrite, nil, ModuleSnapshot{
			ModelDecls:     decls,
			ComputeTargets: map[string]ComputeTarget{"salesmod": {ModelDecls: []model.ModelDeclaration{orderTargetModelDecl()}}},
		})
	insertClient := r.EventInsertClient()

	out, hostErr := ORMCreate(ctx, r, primaryDB, insertClient, nil, mc, abiv1.ORMCreateInput{
		Model: "testmodule.comment",
		Record: map[string]any{
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
