package wasm

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"
	"uuid"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/djangbahevans/goerp/internal/engine/computed"
	"github.com/djangbahevans/goerp/internal/engine/config"
	"github.com/djangbahevans/goerp/internal/engine/fieldsec"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/permission"
	"github.com/djangbahevans/goerp/internal/engine/wasm/wasmtest"
	"github.com/djangbahevans/goerp/sdk/go/model"
	"github.com/djangbahevans/goerp/sdk/go/perm"
)

// orderModelDecl declares a same-record computed field: amount_total
// depends on quantity and unit_price, both present on the same record.
// Field names match testdata/computedfixture's registered
// "_compute_amount_total" function exactly. state is unused by the
// compute-recompute tests but exercised by host_orm_constraint_test.go's
// OnDelete tests, matching the fixture's registered
// ("testmodule.order", orm.OnDelete) constraint hook.
func orderModelDecl() model.ModelDeclaration {
	return model.ModelDeclaration{
		Name: "order",
		Fields: []model.NamedField{
			{Name: "id", Def: model.UUID().Required().PrimaryKey().Default("uuidv7()")},
			{Name: "tenant_id", Def: model.UUID().Required()},
			{Name: "quantity", Def: model.Integer()},
			{Name: "unit_price", Def: model.Integer()},
			{Name: "amount_total", Def: model.BigInt().Computed("_compute_amount_total").Store(true).Depends("quantity", "unit_price")},
			{Name: "state", Def: model.Text()},
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
			{Name: "id", Def: model.UUID().Required().PrimaryKey().Default("uuidv7()")},
			{Name: "tenant_id", Def: model.UUID().Required()},
			{Name: "credit_limit", Def: model.Integer()},
		},
	}
}

func hopOrderModelDecl() model.ModelDeclaration {
	return model.ModelDeclaration{
		Name: "hop_order",
		Fields: []model.NamedField{
			{Name: "id", Def: model.UUID().Required().PrimaryKey().Default("uuidv7()")},
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
		id UUID PRIMARY KEY DEFAULT uuidv7(),
		tenant_id UUID NOT NULL,
		quantity INTEGER,
		unit_price INTEGER,
		amount_total BIGINT,
		state TEXT
	)`); err != nil {
		t.Fatalf("create order table: %v", err)
	}
}

func createFixtureContactAndHopOrderTables(t *testing.T, conn *sql.DB, slug string) {
	t.Helper()
	ctx := context.Background()
	schemaName := "tenant_" + slug

	if _, err := conn.ExecContext(ctx, `CREATE TABLE `+schemaName+`.contact (
		id UUID PRIMARY KEY DEFAULT uuidv7(),
		tenant_id UUID NOT NULL,
		credit_limit INTEGER
	)`); err != nil {
		t.Fatalf("create contact table: %v", err)
	}
	if _, err := conn.ExecContext(ctx, `CREATE TABLE `+schemaName+`.hop_order (
		id UUID PRIMARY KEY DEFAULT uuidv7(),
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
		CompilationCache:            wasmtest.SharedCompilationCacheDir(),
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
	mc := NewModuleContext("req-1", "testmodule", "user-1", "contact-1", []string{"admin"}, nil, uuid.New().String(), slug, "trace-1",
		abi.CapDBRead|abi.CapDBWrite, nil, ModuleSnapshot{
			ModelDecls:     decls,
			ComputedIndex:  idx,
			ComputeTargets: map[string]ComputeTarget{"testmodule": target},
		})

	insertClient := r.EventInsertClient()

	createOut, hostErr := ORMCreate(ctx, r, primaryDB, insertClient, nil, mc, abiv1.ORMCreateInput{
		Model: "testmodule.order",
		Record: map[string]any{
			"quantity": int64(3), "unit_price": int64(25),
		},
	})
	if hostErr != nil {
		t.Fatalf("ORMCreate: %+v", hostErr)
	}
	if got := createOut.Record["amount_total"]; got != int64(75) {
		t.Errorf("amount_total after create = %v, want 75", got)
	}
	orderID, _ := createOut.Record["id"].(string)

	writeOut, hostErr := ORMWrite(ctx, r, primaryDB, insertClient, nil, mc, abiv1.ORMWriteInput{
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
	mc := NewModuleContext("req-1", "testmodule", "user-1", "contact-1", []string{"admin"}, nil, uuid.New().String(), slug, "trace-1",
		abi.CapDBRead|abi.CapDBWrite, nil, ModuleSnapshot{
			ModelDecls:     decls,
			ComputedIndex:  idx,
			ComputeTargets: map[string]ComputeTarget{"testmodule": target},
		})

	insertClient := r.EventInsertClient()

	contactOut, hostErr := ORMCreate(ctx, r, primaryDB, insertClient, nil, mc, abiv1.ORMCreateInput{
		Model:  "testmodule.contact",
		Record: map[string]any{"credit_limit": int64(1000)},
	})
	if hostErr != nil {
		t.Fatalf("create contact: %+v", hostErr)
	}
	contactID, _ := contactOut.Record["id"].(string)

	orderOut, hostErr := ORMCreate(ctx, r, primaryDB, insertClient, nil, mc, abiv1.ORMCreateInput{
		Model:  "testmodule.hop_order",
		Record: map[string]any{"customer_id": contactID},
	})
	if hostErr != nil {
		t.Fatalf("create hop_order: %+v", hostErr)
	}
	orderID, _ := orderOut.Record["id"].(string)

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

	if _, hostErr := ORMWrite(ctx, r, primaryDB, insertClient, nil, mc, abiv1.ORMWriteInput{
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

// A compute function's ORM read runs inside the write transaction, so it sees
// the contact write that triggered the recompute.
func TestRecomputeAfterWrite_Many2OneHopComputeReadsTriggeringWrite(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := t.Context()

	slug := fmt.Sprintf("computehopread%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	createFixtureContactAndHopOrderTables(t, primaryDB, slug)

	r := newComputeTestRuntime(t, primaryDB)
	hopOrder := hopOrderModelDecl()
	for i, f := range hopOrder.Fields {
		if f.Name == "touched_flag" {
			hopOrder.Fields[i].Def = model.BigInt().Computed("_compute_hop_customer_credit").Store(true).Depends("customer.credit_limit")
		}
	}
	decls := []model.ModelDeclaration{contactModelDecl(), hopOrder}

	idx := computed.New()
	idx.Register("testmodule", decls)

	target := newComputeTarget(t, ctx, r, decls)
	target.Capabilities = abi.CapDBRead
	mc := NewModuleContext("req-1", "testmodule", "user-1", "contact-1", []string{"admin"}, nil, uuid.New().String(), slug, "trace-1",
		abi.CapDBRead|abi.CapDBWrite, nil, ModuleSnapshot{
			ModelDecls:     decls,
			ComputedIndex:  idx,
			ComputeTargets: map[string]ComputeTarget{"testmodule": target},
		})

	insertClient := r.EventInsertClient()

	contactOut, hostErr := ORMCreate(ctx, r, primaryDB, insertClient, nil, mc, abiv1.ORMCreateInput{
		Model:  "testmodule.contact",
		Record: map[string]any{"credit_limit": int64(1000)},
	})
	if hostErr != nil {
		t.Fatalf("create contact: %+v", hostErr)
	}
	contactID, _ := contactOut.Record["id"].(string)

	orderOut, hostErr := ORMCreate(ctx, r, primaryDB, insertClient, nil, mc, abiv1.ORMCreateInput{
		Model:  "testmodule.hop_order",
		Record: map[string]any{"customer_id": contactID},
	})
	if hostErr != nil {
		t.Fatalf("create hop_order: %+v", hostErr)
	}
	orderID, _ := orderOut.Record["id"].(string)

	if _, hostErr := ORMWrite(ctx, r, primaryDB, insertClient, nil, mc, abiv1.ORMWriteInput{
		Model:  "testmodule.contact",
		ID:     contactID,
		Record: map[string]any{"credit_limit": int64(2000)},
	}); hostErr != nil {
		t.Fatalf("write contact: %+v", hostErr)
	}

	var got sql.NullInt64
	if err := primaryDB.QueryRow(`SELECT touched_flag FROM tenant_` + slug + `.hop_order WHERE id = '` + orderID + `'`).Scan(&got); err != nil {
		t.Fatalf("query touched_flag: %v", err)
	}
	if !got.Valid || got.Int64 != 2000 {
		t.Errorf("touched_flag after contact write = %+v, want 2000", got)
	}
}

// A failing compute aborts the triggering write and the error names the
// dependent, for a Many2One hop dependent and for a same-record field.
func TestRecomputeAfterWrite_ComputeError_AbortsWriteAndNamesDependent(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := t.Context()

	slug := fmt.Sprintf("computefail%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	createFixtureContactAndHopOrderTables(t, primaryDB, slug)
	createFixtureOrdersTable(t, primaryDB, slug)

	r := newComputeTestRuntime(t, primaryDB)
	hopOrder := hopOrderModelDecl()
	order := orderModelDecl()
	for i, f := range hopOrder.Fields {
		if f.Name == "touched_flag" {
			hopOrder.Fields[i].Def = model.BigInt().Computed("_compute_fail").Store(true).Depends("customer.credit_limit")
		}
	}
	for i, f := range order.Fields {
		if f.Name == "amount_total" {
			order.Fields[i].Def = model.BigInt().Computed("_compute_fail").Store(true).Depends("quantity")
		}
	}
	decls := []model.ModelDeclaration{contactModelDecl(), hopOrder, order}

	idx := computed.New()
	idx.Register("testmodule", decls)

	target := newComputeTarget(t, ctx, r, decls)
	mc := NewModuleContext("req-1", "testmodule", "user-1", "contact-1", []string{"admin"}, nil, uuid.New().String(), slug, "trace-1",
		abi.CapDBRead|abi.CapDBWrite, nil, ModuleSnapshot{
			ModelDecls:     decls,
			ComputedIndex:  idx,
			ComputeTargets: map[string]ComputeTarget{"testmodule": target},
		})
	insertClient := r.EventInsertClient()
	schema := "tenant_" + slug
	contactID, hopOrderID, orderID := uuid.New().String(), uuid.New().String(), uuid.New().String()

	for _, stmt := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO ` + schema + `.contact (id, tenant_id, credit_limit) VALUES ($1, $2, 1000)`, []any{contactID, mc.TenantID}},
		{`INSERT INTO ` + schema + `.hop_order (id, tenant_id, customer_id) VALUES ($1, $2, $3)`, []any{hopOrderID, mc.TenantID, contactID}},
		{`INSERT INTO ` + schema + `.order (id, tenant_id, quantity) VALUES ($1, $2, 1)`, []any{orderID, mc.TenantID}},
	} {
		if _, err := primaryDB.ExecContext(ctx, stmt.sql, stmt.args...); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	tests := []struct {
		name         string
		write        abiv1.ORMWriteInput
		readOriginal string
		wantOriginal int64
		wantModel    string
		wantField    string
		wantID       string
	}{
		{
			name:         "hop dependent",
			write:        abiv1.ORMWriteInput{Model: "testmodule.contact", ID: contactID, Record: map[string]any{"credit_limit": int64(2000)}},
			readOriginal: `SELECT credit_limit FROM ` + schema + `.contact WHERE id = '` + contactID + `'`,
			wantOriginal: 1000,
			wantModel:    "testmodule.hop_order",
			wantField:    "touched_flag",
			wantID:       hopOrderID,
		},
		{
			name:         "same record",
			write:        abiv1.ORMWriteInput{Model: "testmodule.order", ID: orderID, Record: map[string]any{"quantity": int64(5)}},
			readOriginal: `SELECT quantity FROM ` + schema + `.order WHERE id = '` + orderID + `'`,
			wantOriginal: 1,
			wantModel:    "testmodule.order",
			wantField:    "amount_total",
			wantID:       orderID,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, hostErr := ORMWrite(ctx, r, primaryDB, insertClient, nil, mc, tt.write)
			if hostErr == nil {
				t.Fatal("write succeeded, want the failing compute to abort it")
			}
			for _, want := range []string{tt.wantModel + "." + tt.wantField, tt.wantID, "compute failed"} {
				if !strings.Contains(hostErr.Message, want) {
					t.Errorf("message = %q, want it to contain %q", hostErr.Message, want)
				}
			}
			if hostErr.Details["model"] != tt.wantModel || hostErr.Details["field"] != tt.wantField || hostErr.Details["record_id"] != tt.wantID {
				t.Errorf("details = %v, want model %s, field %s, record_id %s", hostErr.Details, tt.wantModel, tt.wantField, tt.wantID)
			}
			var stored int64
			if err := primaryDB.QueryRowContext(ctx, tt.readOriginal).Scan(&stored); err != nil {
				t.Fatalf("read the written column: %v", err)
			}
			if stored != tt.wantOriginal {
				t.Errorf("written column = %d after the aborted write, want %d", stored, tt.wantOriginal)
			}
		})
	}
}

// A compute function's ORM reads ignore the caller's field-level read rules,
// so the stored value does not depend on which user triggered the recompute.
func TestRecomputeAfterWrite_ComputeReadsIgnoreCallerFieldReadRules(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := t.Context()

	slug := fmt.Sprintf("computeunmasked%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	createFixtureContactAndHopOrderTables(t, primaryDB, slug)

	r := newComputeTestRuntime(t, primaryDB)
	contact := contactModelDecl()
	for i, f := range contact.Fields {
		if f.Name == "credit_limit" {
			contact.Fields[i].Def = model.Integer().Access(model.AccessRead(perm.Ref("contacts:contact:financials_read")))
		}
	}
	hopOrder := hopOrderModelDecl()
	for i, f := range hopOrder.Fields {
		if f.Name == "touched_flag" {
			hopOrder.Fields[i].Def = model.BigInt().Computed("_compute_hop_customer_credit").Store(true).Depends("customer.credit_limit")
		}
	}
	decls := []model.ModelDeclaration{contact, hopOrder}

	idx := computed.New()
	idx.Register("testmodule", decls)
	fieldSecReg := fieldsec.New()
	fieldSecReg.Register("testmodule", decls)
	permReg := permission.NewPermissionRegistry()
	permReg.Register("testmodule", []manifest.Permission{{Name: "contacts:contact:financials_read"}})

	target := newComputeTarget(t, ctx, r, decls)
	target.Capabilities = abi.CapDBRead
	mc := NewModuleContext("req-1", "testmodule", "user-1", "contact-1", []string{"admin"}, permission.PermissionBitfield{}, uuid.New().String(), slug, "trace-1",
		abi.CapDBRead|abi.CapDBWrite, nil, ModuleSnapshot{
			ModelDecls:         decls,
			ComputedIndex:      idx,
			FieldSecRegistry:   fieldSecReg,
			PermissionRegistry: permReg,
			ComputeTargets:     map[string]ComputeTarget{"testmodule": target},
		})
	insertClient := r.EventInsertClient()

	contactOut, hostErr := ORMCreate(ctx, r, primaryDB, insertClient, nil, mc, abiv1.ORMCreateInput{
		Model:  "testmodule.contact",
		Record: map[string]any{"credit_limit": int64(1000)},
	})
	if hostErr != nil {
		t.Fatalf("create contact: %+v", hostErr)
	}
	contactID, _ := contactOut.Record["id"].(string)
	orderOut, hostErr := ORMCreate(ctx, r, primaryDB, insertClient, nil, mc, abiv1.ORMCreateInput{
		Model:  "testmodule.hop_order",
		Record: map[string]any{"customer_id": contactID},
	})
	if hostErr != nil {
		t.Fatalf("create hop_order: %+v", hostErr)
	}
	orderID, _ := orderOut.Record["id"].(string)

	if _, hostErr := ORMWrite(ctx, r, primaryDB, insertClient, nil, mc, abiv1.ORMWriteInput{
		Model: "testmodule.contact", ID: contactID, Record: map[string]any{"credit_limit": int64(2000)},
	}); hostErr != nil {
		t.Fatalf("write contact: %+v", hostErr)
	}

	var got sql.NullInt64
	if err := primaryDB.QueryRowContext(ctx, `SELECT touched_flag FROM tenant_`+slug+`.hop_order WHERE id = $1`, orderID).Scan(&got); err != nil {
		t.Fatalf("query touched_flag: %v", err)
	}
	if !got.Valid || got.Int64 != 2000 {
		t.Errorf("touched_flag = %+v, want 2000: the compute read must see credit_limit although the caller lacks its read permission", got)
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
	mc := NewModuleContext("req-1", "testmodule", "user-1", "contact-1", []string{"admin"}, nil, uuid.New().String(), slug, "trace-1",
		abi.CapDBRead|abi.CapDBWrite, nil, ModuleSnapshot{ModelDecls: decls})

	insertClient := r.EventInsertClient()

	_, hostErr := ORMCreate(ctx, r, primaryDB, insertClient, nil, mc, abiv1.ORMCreateInput{
		Model: "testmodule.order",
		Record: map[string]any{
			"quantity": int64(1), "unit_price": int64(1), "amount_total": int64(999),
		},
	})
	if hostErr == nil || hostErr.Code != abiv1.ErrCodeFieldNotWritable {
		t.Fatalf("ORMCreate with a computed field in payload: hostErr = %+v, want code %s", hostErr, abiv1.ErrCodeFieldNotWritable)
	}
}
