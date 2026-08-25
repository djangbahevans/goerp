package wasm

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/djangbahevans/goerp/internal/engine/computed"
	"github.com/djangbahevans/goerp/internal/engine/config"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

// orderModelDecl declares a same-record computed field: amount_total
// depends on quantity and unit_price, both present on the same record.
// Field names match testdata/computedfixture's registered
// "_compute_amount_total" function exactly.
func orderModelDecl() model.ModelDeclaration {
	return model.ModelDeclaration{
		Name: "order",
		Fields: []model.NamedField{
			{Name: "id", Def: model.UUID().Required().PrimaryKey()},
			{Name: "tenant_id", Def: model.UUID().Required()},
			{Name: "quantity", Def: model.Integer()},
			{Name: "unit_price", Def: model.Integer()},
			{Name: "amount_total", Def: model.BigInt().Computed("_compute_amount_total").Store(true).Depends("quantity", "unit_price")},
		},
	}
}

// contactModelDecl and hopOrderModelDecl exercise the Many2One-hop case:
// writing contact.credit_limit must recompute hop_order.touched_flag for
// every hop_order row whose customer_id points at the written contact.
func contactModelDecl() model.ModelDeclaration {
	return model.ModelDeclaration{
		Name: "contact",
		Fields: []model.NamedField{
			{Name: "id", Def: model.UUID().Required().PrimaryKey()},
			{Name: "tenant_id", Def: model.UUID().Required()},
			{Name: "credit_limit", Def: model.Integer()},
		},
	}
}

func hopOrderModelDecl() model.ModelDeclaration {
	return model.ModelDeclaration{
		Name: "hop_order",
		Fields: []model.NamedField{
			{Name: "id", Def: model.UUID().Required().PrimaryKey()},
			{Name: "tenant_id", Def: model.UUID().Required()},
			{Name: "customer_id", Def: model.Many2One("testmodule.contact")},
			{Name: "touched_flag", Def: model.BigInt().Computed("_compute_hop_marker").Store(true).Depends("customer.credit_limit")},
		},
	}
}

func createFixtureOrdersTable(t *testing.T, conn *sql.DB, slug string) {
	t.Helper()
	ctx := context.Background()
	schemaName := "tenant_" + slug

	if _, err := conn.ExecContext(ctx, `CREATE TABLE `+schemaName+`.order (
		id UUID PRIMARY KEY,
		tenant_id UUID NOT NULL,
		quantity INTEGER,
		unit_price INTEGER,
		amount_total BIGINT
	)`); err != nil {
		t.Fatalf("create order table: %v", err)
	}
}

func createFixtureContactAndHopOrderTables(t *testing.T, conn *sql.DB, slug string) {
	t.Helper()
	ctx := context.Background()
	schemaName := "tenant_" + slug

	if _, err := conn.ExecContext(ctx, `CREATE TABLE `+schemaName+`.contact (
		id UUID PRIMARY KEY,
		tenant_id UUID NOT NULL,
		credit_limit INTEGER
	)`); err != nil {
		t.Fatalf("create contact table: %v", err)
	}
	if _, err := conn.ExecContext(ctx, `CREATE TABLE `+schemaName+`.hop_order (
		id UUID PRIMARY KEY,
		tenant_id UUID NOT NULL,
		customer_id UUID,
		touched_flag BIGINT
	)`); err != nil {
		t.Fatalf("create hop_order table: %v", err)
	}
}

// newComputeTestRuntime builds a Runtime with enough memory headroom for a
// real compiled wasip1 module (instance_compute_test.go's own
// TestInvokeHandleComputed_RoundTripsThroughRealModule notes the 1 MiB cap
// newHostDBTestRuntime otherwise uses is too small for a real c-shared
// binary).
func newComputeTestRuntime(t *testing.T, primaryDB *sql.DB) *Runtime {
	t.Helper()

	rt, err := New(&config.Config{
		CompilationCache:            filepath.Join(t.TempDir(), "cache"),
		Environment:                 string(config.Production),
		PoolMaxMemoryByes:           64 << 20,
		DBMaxConcurrentTransactions: 10,
	}, primaryDB, nil, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = rt.Close(context.Background()) })
	return rt
}

// newComputeTarget builds a ComputeTarget backed by a real pool of the
// compiled testdata/computedfixture module.
func newComputeTarget(t *testing.T, ctx context.Context, r *Runtime, decls []model.ModelDeclaration) ComputeTarget {
	t.Helper()

	wasmBytes := compileComputedFixture(t)
	compiled, err := r.CompileModule(ctx, wasmBytes)
	if err != nil {
		t.Fatalf("CompileModule: %v", err)
	}
	t.Cleanup(func() { _ = compiled.Close(ctx) })

	pool := r.NewPool("computedfixture", compiled, PoolConfig{})
	t.Cleanup(func() { pool.DrainAndClose(context.Background(), 5*time.Second) })

	return ComputeTarget{Pool: pool, ModelDecls: decls}
}

func TestRecomputeAfterWrite_SameRecordDependency(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("computesame%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	createFixtureOrdersTable(t, primaryDB, slug)

	r := newComputeTestRuntime(t, primaryDB)
	decls := []model.ModelDeclaration{orderModelDecl()}

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

	orderID := "10000000-0000-0000-0000-000000000001"
	createOut, hostErr := ORMCreate(ctx, r, primaryDB, insertClient, nil, mc, ORMCreateInput{
		Model: "testmodule.order",
		Record: map[string]any{
			"id": orderID, "tenant_id": "00000000-0000-0000-0000-000000000001",
			"quantity": int64(3), "unit_price": int64(25),
		},
	})
	if hostErr != nil {
		t.Fatalf("ORMCreate: %+v", hostErr)
	}
	if got := createOut.Record["amount_total"]; got != int64(75) {
		t.Errorf("amount_total after create = %v, want 75", got)
	}

	writeOut, hostErr := ORMWrite(ctx, r, primaryDB, insertClient, nil, mc, ORMWriteInput{
		Model:  "testmodule.order",
		ID:     orderID,
		Record: map[string]any{"quantity": int64(4)},
	})
	if hostErr != nil {
		t.Fatalf("ORMWrite: %+v", hostErr)
	}
	if got := writeOut.Record["amount_total"]; got != int64(100) {
		t.Errorf("amount_total after write = %v, want 100 (4 * 25)", got)
	}
}

func TestRecomputeAfterWrite_Many2OneHopDependency(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("computehop%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	createFixtureContactAndHopOrderTables(t, primaryDB, slug)

	r := newComputeTestRuntime(t, primaryDB)
	decls := []model.ModelDeclaration{contactModelDecl(), hopOrderModelDecl()}

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
	contactID := "20000000-0000-0000-0000-000000000001"
	orderID := "20000000-0000-0000-0000-000000000002"

	if _, hostErr := ORMCreate(ctx, r, primaryDB, insertClient, nil, mc, ORMCreateInput{
		Model:  "testmodule.contact",
		Record: map[string]any{"id": contactID, "tenant_id": tenantID, "credit_limit": int64(1000)},
	}); hostErr != nil {
		t.Fatalf("create contact: %+v", hostErr)
	}
	if _, hostErr := ORMCreate(ctx, r, primaryDB, insertClient, nil, mc, ORMCreateInput{
		Model:  "testmodule.hop_order",
		Record: map[string]any{"id": orderID, "tenant_id": tenantID, "customer_id": contactID},
	}); hostErr != nil {
		t.Fatalf("create hop_order: %+v", hostErr)
	}

	// touched_flag starts unset — only writing the *related* contact
	// (not the order itself) should trigger recompute, through the
	// customer_id hop.
	var before sql.NullInt64
	if err := primaryDB.QueryRow(`SELECT touched_flag FROM tenant_` + slug + `.hop_order WHERE id = '` + orderID + `'`).Scan(&before); err != nil {
		t.Fatalf("query touched_flag before: %v", err)
	}
	if before.Valid {
		t.Fatalf("touched_flag before contact write = %v, want NULL", before.Int64)
	}

	if _, hostErr := ORMWrite(ctx, r, primaryDB, insertClient, nil, mc, ORMWriteInput{
		Model:  "testmodule.contact",
		ID:     contactID,
		Record: map[string]any{"credit_limit": int64(2000)},
	}); hostErr != nil {
		t.Fatalf("write contact: %+v", hostErr)
	}

	var after sql.NullInt64
	if err := primaryDB.QueryRow(`SELECT touched_flag FROM tenant_` + slug + `.hop_order WHERE id = '` + orderID + `'`).Scan(&after); err != nil {
		t.Fatalf("query touched_flag after: %v", err)
	}
	if !after.Valid || after.Int64 != 1 {
		t.Errorf("touched_flag after contact write = %+v, want 1", after)
	}
}

func TestORMWrite_ComputedField_RejectedAsFieldNotWritable(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("computereject%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	createFixtureOrdersTable(t, primaryDB, slug)

	r := newComputeTestRuntime(t, primaryDB)
	decls := []model.ModelDeclaration{orderModelDecl()}
	mc := NewModuleContext("req-1", "testmodule", "user-1", "contact-1", []string{"admin"}, slug, slug, "trace-1",
		abi.CapDBRead|abi.CapDBWrite, nil, ModuleSnapshot{ModelDecls: decls})

	insertClient := r.EventInsertClient()
	orderID := "30000000-0000-0000-0000-000000000001"

	_, hostErr := ORMCreate(ctx, r, primaryDB, insertClient, nil, mc, ORMCreateInput{
		Model: "testmodule.order",
		Record: map[string]any{
			"id": orderID, "tenant_id": "00000000-0000-0000-0000-000000000001",
			"quantity": int64(1), "unit_price": int64(1), "amount_total": int64(999),
		},
	})
	if hostErr == nil || hostErr.Code != abi.ErrCodeFieldNotWritable {
		t.Fatalf("ORMCreate with a computed field in payload: hostErr = %+v, want code %s", hostErr, abi.ErrCodeFieldNotWritable)
	}
}
