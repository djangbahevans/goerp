package wasm

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/djangbahevans/goerp/internal/engine/computed"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

// lineOrderModelDecl and orderLineFixtureModelDecl exercise the One2Many-hop
// case (goerp#388): creating/writing/unlinking an order_line row must
// recompute line_order.lines_total, resolved via the "lines" One2Many
// field's inverse ("order_id" on order_line). lines_total reuses the
// fixture's existing "_compute_hop_marker" function (always returns 1) —
// the same constant-marker technique host_orm_compute_test.go's own
// Many2One-hop test uses, since these tests only need to observe whether
// recompute fired, not verify real aggregation math.
func lineOrderModelDecl() model.ModelDeclaration {
	return model.ModelDeclaration{
		Name: "line_order",
		Fields: []model.NamedField{
			{Name: "id", Def: model.UUID().Required().PrimaryKey()},
			{Name: "tenant_id", Def: model.UUID().Required()},
			{Name: "lines", Def: model.One2Many("testmodule.order_line", "order_id")},
			{Name: "lines_total", Def: model.BigInt().Computed("_compute_hop_marker").Store(true).Depends("lines.quantity")},
		},
	}
}

func orderLineFixtureModelDecl() model.ModelDeclaration {
	return model.ModelDeclaration{
		Name: "order_line",
		Fields: []model.NamedField{
			{Name: "id", Def: model.UUID().Required().PrimaryKey()},
			{Name: "tenant_id", Def: model.UUID().Required()},
			{Name: "order_id", Def: model.Many2One("testmodule.line_order")},
			{Name: "quantity", Def: model.Integer()},
		},
	}
}

func createFixtureLineOrderTables(t *testing.T, conn *sql.DB, slug string) {
	t.Helper()
	ctx := context.Background()
	schemaName := "tenant_" + slug

	if _, err := conn.ExecContext(ctx, `CREATE TABLE `+schemaName+`.line_order (
		id UUID PRIMARY KEY,
		tenant_id UUID NOT NULL,
		lines_total BIGINT
	)`); err != nil {
		t.Fatalf("create line_order table: %v", err)
	}
	if _, err := conn.ExecContext(ctx, `CREATE TABLE `+schemaName+`.order_line (
		id UUID PRIMARY KEY,
		tenant_id UUID NOT NULL,
		order_id UUID,
		quantity INTEGER
	)`); err != nil {
		t.Fatalf("create order_line table: %v", err)
	}
}

func TestRecomputeAfterWrite_One2ManyHopDependency_OnChildCreate(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("computeviachildcreate%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	createFixtureLineOrderTables(t, primaryDB, slug)

	r := newComputeTestRuntime(t, primaryDB)
	decls := []model.ModelDeclaration{lineOrderModelDecl(), orderLineFixtureModelDecl()}

	idx := computed.New()
	idx.Register("testmodule", decls)

	target := newComputeTarget(t, ctx, r, decls)
	mc := NewModuleContext("req-1", "testmodule", "user-1", "contact-1", []string{"admin"}, slug, slug, "trace-1",
		abi.CapDBRead|abi.CapDBWrite, nil, ModuleSnapshot{
			ModelDecls:     decls,
			ComputedIndex:  idx,
			ComputeTargets: map[string]ComputeTarget{"testmodule": target},
		})

	insertClient := r.EventInsertClient()
	tenantID := "00000000-0000-0000-0000-000000000001"
	parentID := "30000000-0000-0000-0000-000000000001"
	lineID := "30000000-0000-0000-0000-000000000002"

	if _, hostErr := ORMCreate(ctx, r, primaryDB, insertClient, nil, mc, ORMCreateInput{
		Model:  "testmodule.line_order",
		Record: map[string]any{"id": parentID, "tenant_id": tenantID},
	}); hostErr != nil {
		t.Fatalf("create line_order: %+v", hostErr)
	}

	var before sql.NullInt64
	if err := primaryDB.QueryRow(`SELECT lines_total FROM tenant_` + slug + `.line_order WHERE id = '` + parentID + `'`).Scan(&before); err != nil {
		t.Fatalf("query lines_total before: %v", err)
	}
	if before.Valid {
		t.Fatalf("lines_total before child create = %v, want NULL", before.Int64)
	}

	if _, hostErr := ORMCreate(ctx, r, primaryDB, insertClient, nil, mc, ORMCreateInput{
		Model:  "testmodule.order_line",
		Record: map[string]any{"id": lineID, "tenant_id": tenantID, "order_id": parentID, "quantity": int64(5)},
	}); hostErr != nil {
		t.Fatalf("create order_line: %+v", hostErr)
	}

	var after sql.NullInt64
	if err := primaryDB.QueryRow(`SELECT lines_total FROM tenant_` + slug + `.line_order WHERE id = '` + parentID + `'`).Scan(&after); err != nil {
		t.Fatalf("query lines_total after: %v", err)
	}
	if !after.Valid || after.Int64 != 1 {
		t.Errorf("lines_total after child create = %+v, want 1", after)
	}
}

func TestRecomputeAfterWrite_One2ManyHopDependency_OnChildWrite(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("computeviachildwrite%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	createFixtureLineOrderTables(t, primaryDB, slug)

	r := newComputeTestRuntime(t, primaryDB)
	decls := []model.ModelDeclaration{lineOrderModelDecl(), orderLineFixtureModelDecl()}

	idx := computed.New()
	idx.Register("testmodule", decls)

	target := newComputeTarget(t, ctx, r, decls)
	mc := NewModuleContext("req-1", "testmodule", "user-1", "contact-1", []string{"admin"}, slug, slug, "trace-1",
		abi.CapDBRead|abi.CapDBWrite, nil, ModuleSnapshot{
			ModelDecls:     decls,
			ComputedIndex:  idx,
			ComputeTargets: map[string]ComputeTarget{"testmodule": target},
		})

	insertClient := r.EventInsertClient()
	tenantID := "00000000-0000-0000-0000-000000000001"
	parentID := "31000000-0000-0000-0000-000000000001"
	lineID := "31000000-0000-0000-0000-000000000002"

	if _, hostErr := ORMCreate(ctx, r, primaryDB, insertClient, nil, mc, ORMCreateInput{
		Model:  "testmodule.line_order",
		Record: map[string]any{"id": parentID, "tenant_id": tenantID},
	}); hostErr != nil {
		t.Fatalf("create line_order: %+v", hostErr)
	}
	if _, hostErr := ORMCreate(ctx, r, primaryDB, insertClient, nil, mc, ORMCreateInput{
		Model:  "testmodule.order_line",
		Record: map[string]any{"id": lineID, "tenant_id": tenantID, "order_id": parentID, "quantity": int64(5)},
	}); hostErr != nil {
		t.Fatalf("create order_line: %+v", hostErr)
	}

	// Reset lines_total to NULL directly (bypassing recompute) so a
	// subsequent child write's own recompute is the only thing that could
	// flip it back — isolates "did the write trigger recompute" from the
	// create above, since the fixture's compute function always returns
	// the same constant and can't otherwise distinguish "recomputed
	// again" from "still holding its value from create."
	if _, err := primaryDB.Exec(`UPDATE tenant_` + slug + `.line_order SET lines_total = NULL WHERE id = '` + parentID + `'`); err != nil {
		t.Fatalf("reset lines_total: %v", err)
	}

	if _, hostErr := ORMWrite(ctx, r, primaryDB, insertClient, nil, mc, ORMWriteInput{
		Model:  "testmodule.order_line",
		ID:     lineID,
		Record: map[string]any{"quantity": int64(9)},
	}); hostErr != nil {
		t.Fatalf("write order_line: %+v", hostErr)
	}

	var after sql.NullInt64
	if err := primaryDB.QueryRow(`SELECT lines_total FROM tenant_` + slug + `.line_order WHERE id = '` + parentID + `'`).Scan(&after); err != nil {
		t.Fatalf("query lines_total after: %v", err)
	}
	if !after.Valid || after.Int64 != 1 {
		t.Errorf("lines_total after child write = %+v, want 1", after)
	}
}

func TestRecomputeAfterWrite_One2ManyHopDependency_OnChildUnlink(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("computeviachildunlink%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	createFixtureLineOrderTables(t, primaryDB, slug)

	r := newComputeTestRuntime(t, primaryDB)
	decls := []model.ModelDeclaration{lineOrderModelDecl(), orderLineFixtureModelDecl()}

	idx := computed.New()
	idx.Register("testmodule", decls)

	target := newComputeTarget(t, ctx, r, decls)
	mc := NewModuleContext("req-1", "testmodule", "user-1", "contact-1", []string{"admin"}, slug, slug, "trace-1",
		abi.CapDBRead|abi.CapDBWrite, nil, ModuleSnapshot{
			ModelDecls:     decls,
			ComputedIndex:  idx,
			ComputeTargets: map[string]ComputeTarget{"testmodule": target},
		})

	insertClient := r.EventInsertClient()
	tenantID := "00000000-0000-0000-0000-000000000001"
	parentID := "32000000-0000-0000-0000-000000000001"
	lineID := "32000000-0000-0000-0000-000000000002"

	if _, hostErr := ORMCreate(ctx, r, primaryDB, insertClient, nil, mc, ORMCreateInput{
		Model:  "testmodule.line_order",
		Record: map[string]any{"id": parentID, "tenant_id": tenantID},
	}); hostErr != nil {
		t.Fatalf("create line_order: %+v", hostErr)
	}
	if _, hostErr := ORMCreate(ctx, r, primaryDB, insertClient, nil, mc, ORMCreateInput{
		Model:  "testmodule.order_line",
		Record: map[string]any{"id": lineID, "tenant_id": tenantID, "order_id": parentID, "quantity": int64(5)},
	}); hostErr != nil {
		t.Fatalf("create order_line: %+v", hostErr)
	}

	// Same reset-to-NULL technique as the write test above — isolates
	// whether ORMUnlink's own recompute call fired.
	if _, err := primaryDB.Exec(`UPDATE tenant_` + slug + `.line_order SET lines_total = NULL WHERE id = '` + parentID + `'`); err != nil {
		t.Fatalf("reset lines_total: %v", err)
	}

	if _, hostErr := ORMUnlink(ctx, r, primaryDB, insertClient, nil, mc, ORMUnlinkInput{
		Model: "testmodule.order_line",
		ID:    lineID,
	}); hostErr != nil {
		t.Fatalf("unlink order_line: %+v", hostErr)
	}

	var after sql.NullInt64
	if err := primaryDB.QueryRow(`SELECT lines_total FROM tenant_` + slug + `.line_order WHERE id = '` + parentID + `'`).Scan(&after); err != nil {
		t.Fatalf("query lines_total after: %v", err)
	}
	if !after.Valid || after.Int64 != 1 {
		t.Errorf("lines_total after child unlink = %+v, want 1", after)
	}
}

func TestRecomputeAfterWrite_One2ManyHopDependency_ChildWithoutParent_NoOp(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("computeviachildorphan%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	createFixtureLineOrderTables(t, primaryDB, slug)

	r := newComputeTestRuntime(t, primaryDB)
	decls := []model.ModelDeclaration{lineOrderModelDecl(), orderLineFixtureModelDecl()}

	idx := computed.New()
	idx.Register("testmodule", decls)

	target := newComputeTarget(t, ctx, r, decls)
	mc := NewModuleContext("req-1", "testmodule", "user-1", "contact-1", []string{"admin"}, slug, slug, "trace-1",
		abi.CapDBRead|abi.CapDBWrite, nil, ModuleSnapshot{
			ModelDecls:     decls,
			ComputedIndex:  idx,
			ComputeTargets: map[string]ComputeTarget{"testmodule": target},
		})

	insertClient := r.EventInsertClient()
	tenantID := "00000000-0000-0000-0000-000000000001"
	lineID := "33000000-0000-0000-0000-000000000001"

	// order_id is omitted — an orphan child not linked to any parent yet.
	if _, hostErr := ORMCreate(ctx, r, primaryDB, insertClient, nil, mc, ORMCreateInput{
		Model:  "testmodule.order_line",
		Record: map[string]any{"id": lineID, "tenant_id": tenantID, "quantity": int64(5)},
	}); hostErr != nil {
		t.Fatalf("create orphan order_line: %+v", hostErr)
	}
}
