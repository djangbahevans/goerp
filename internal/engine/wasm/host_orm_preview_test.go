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

// pricedOrderModelDecl declares a Store(true) computed field
// (computed_flag, via _compute_hop_marker) alongside price_list_id, a
// plain field only ever set by testdata/computedfixture's registered
// PreviewHook for "testmodule.priced_order" — enough surface to prove
// ORMPreview runs the .Depends() pass before the hook, per
// go-sdk-reference.md §22 "Preview action".
func pricedOrderModelDecl() model.ModelDeclaration {
	return model.ModelDeclaration{
		Name: "priced_order",
		Fields: []model.NamedField{
			{Name: "id", Def: model.UUID().Required().PrimaryKey()},
			{Name: "tenant_id", Def: model.UUID().Required()},
			{Name: "customer_id", Def: model.UUID()},
			{Name: "computed_flag", Def: model.BigInt().Computed("_compute_hop_marker").Store(true).Depends("customer_id")},
			{Name: "price_list_id", Def: model.Text()},
		},
	}
}

func createFixturePricedOrdersTable(t *testing.T, conn *sql.DB, slug string) {
	t.Helper()
	ctx := context.Background()
	schemaName := "tenant_" + slug

	if _, err := conn.ExecContext(ctx, `CREATE TABLE `+schemaName+`.priced_order (
		id UUID PRIMARY KEY,
		tenant_id UUID NOT NULL,
		customer_id UUID,
		computed_flag BIGINT,
		price_list_id TEXT
	)`); err != nil {
		t.Fatalf("create priced_order table: %v", err)
	}
}

func countRows(t *testing.T, conn *sql.DB, schema, table string) int {
	t.Helper()
	var count int
	if err := conn.QueryRow(`SELECT COUNT(*) FROM ` + schema + `.` + table).Scan(&count); err != nil {
		t.Fatalf("count %s.%s: %v", schema, table, err)
	}
	return count
}

func TestORMPreview_DependsOnlyRecompute_NoRowWritten(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("previewdeps%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	createFixtureOrdersTable(t, primaryDB, slug)

	r := newComputeTestRuntime(t, primaryDB)
	decls := []model.ModelDeclaration{orderModelDecl()}
	target := newComputeTarget(t, ctx, r, decls)
	mc := NewModuleContext("req-1", "testmodule", "user-1", "contact-1", []string{"admin"}, slug, slug, "trace-1",
		abi.CapDBRead|abi.CapDBWrite, nil, ModuleSnapshot{
			ModelDecls:     decls,
			ComputeTargets: map[string]ComputeTarget{"testmodule": target},
		})

	out, hostErr := ORMPreview(ctx, r, mc, ORMPreviewInput{
		Model:  "testmodule.order",
		Record: map[string]any{"quantity": int64(3), "unit_price": int64(25)},
	})
	if hostErr != nil {
		t.Fatalf("ORMPreview: %+v", hostErr)
	}
	if got := out.Record["amount_total"]; got != int64(75) {
		t.Errorf("amount_total = %v, want 75", got)
	}

	if got := countRows(t, primaryDB, "tenant_"+slug, "order"); got != 0 {
		t.Errorf("order table has %d rows after preview, want 0 (nothing should ever be persisted)", got)
	}
}

func TestORMPreview_ComputedDependencyAbsent_LeftUnset(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("previewabsent%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	createFixtureOrdersTable(t, primaryDB, slug)

	r := newComputeTestRuntime(t, primaryDB)
	decls := []model.ModelDeclaration{orderModelDecl()}
	target := newComputeTarget(t, ctx, r, decls)
	mc := NewModuleContext("req-1", "testmodule", "user-1", "contact-1", []string{"admin"}, slug, slug, "trace-1",
		abi.CapDBRead|abi.CapDBWrite, nil, ModuleSnapshot{
			ModelDecls:     decls,
			ComputeTargets: map[string]ComputeTarget{"testmodule": target},
		})

	// unit_price is missing — amount_total's dependencies aren't fully
	// present, so it must not be recomputed (and invokeCompute/the
	// fixture's compute function is never even called).
	out, hostErr := ORMPreview(ctx, r, mc, ORMPreviewInput{
		Model:  "testmodule.order",
		Record: map[string]any{"quantity": int64(3)},
	})
	if hostErr != nil {
		t.Fatalf("ORMPreview: %+v", hostErr)
	}
	if _, present := out.Record["amount_total"]; present {
		t.Errorf("amount_total = %v, want absent (dependency unit_price missing from draft)", out.Record["amount_total"])
	}
}

func TestORMPreview_RegisteredHook_RunsAfterDependsRecompute(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("previewhook%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	createFixturePricedOrdersTable(t, primaryDB, slug)

	r := newComputeTestRuntime(t, primaryDB)
	decls := []model.ModelDeclaration{pricedOrderModelDecl()}
	target := newComputeTarget(t, ctx, r, decls)
	mc := NewModuleContext("req-1", "testmodule", "user-1", "contact-1", []string{"admin"}, slug, slug, "trace-1",
		abi.CapDBRead|abi.CapDBWrite, nil, ModuleSnapshot{
			ModelDecls:     decls,
			ComputeTargets: map[string]ComputeTarget{"testmodule": target},
		})

	out, hostErr := ORMPreview(ctx, r, mc, ORMPreviewInput{
		Model:  "testmodule.priced_order",
		Record: map[string]any{"id": "order-1", "customer_id": "cust-1"},
	})
	if hostErr != nil {
		t.Fatalf("ORMPreview: %+v", hostErr)
	}
	if got := out.Record["computed_flag"]; got != int64(1) {
		t.Errorf("computed_flag = %v, want 1 (from .Depends() recompute)", got)
	}
	if got := out.Record["price_list_id"]; got != "list-"+slug {
		t.Errorf("price_list_id = %v, want %q (from the registered PreviewHook)", got, "list-"+slug)
	}

	if got := countRows(t, primaryDB, "tenant_"+slug, "priced_order"); got != 0 {
		t.Errorf("priced_order table has %d rows after preview, want 0", got)
	}
}
