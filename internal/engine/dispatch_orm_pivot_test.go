package engine

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/djangbahevans/goerp/internal/engine/auth/authcheck"
	"github.com/djangbahevans/goerp/internal/engine/config"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/registry"
	"github.com/djangbahevans/goerp/internal/engine/route"
	tenantresolve "github.com/djangbahevans/goerp/internal/engine/tenant/resolve"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
	"github.com/djangbahevans/goerp/internal/engine/wasm"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

// pivotFixtureModelDecl declares a "sale" model with two categorical
// dimensions (region, category), one plain numeric value (amount), and
// one read-restricted numeric value (cost) — enough to exercise
// multi-level ROLLUP, every aggregation function, and the field-security
// rejection path in one fixture.
func pivotFixtureModelDecl() model.ModelDeclaration {
	return model.ModelDeclaration{
		Name:  "sale",
		Table: "sale",
		Fields: []model.NamedField{
			{Name: "id", Def: model.UUID().Required().PrimaryKey()},
			{Name: "region", Def: model.Text()},
			{Name: "category", Def: model.Text()},
			{Name: "amount", Def: model.Integer()},
			{Name: "cost", Def: model.Integer().
				Access(model.AccessRead("testmodule:sale:cost_read")).
				OnDeniedRead(model.Omit)},
		},
		EnabledOps: []model.Op{model.Pivot},
	}
}

func createFixtureSaleSchema(t *testing.T, conn *sql.DB, slug string) {
	t.Helper()
	ctx := context.Background()
	schemaName := tenantschema.Name(slug)

	if _, err := conn.ExecContext(ctx, "CREATE SCHEMA "+schemaName); err != nil {
		t.Fatalf("create fixture schema: %v", err)
	}
	t.Cleanup(func() {
		_, _ = conn.ExecContext(context.Background(), "DROP SCHEMA "+schemaName+" CASCADE")
	})

	if _, err := conn.ExecContext(ctx, `CREATE TABLE `+schemaName+`.sale (
		id UUID PRIMARY KEY,
		region TEXT,
		category TEXT,
		amount INTEGER,
		cost INTEGER
	)`); err != nil {
		t.Fatalf("create sale table: %v", err)
	}

	rows := []struct {
		region, category   string
		amount, cost, seed int
	}{
		{"east", "widgets", 100, 40, 1},
		{"east", "widgets", 50, 20, 2},
		{"east", "gadgets", 30, 10, 3},
		{"west", "widgets", 200, 80, 4},
		{"west", "gadgets", 70, 30, 5},
	}
	for _, r := range rows {
		id := fmt.Sprintf("00000000-0000-0000-0000-%012d", r.seed)
		if _, err := conn.ExecContext(ctx, `INSERT INTO `+schemaName+`.sale (id, region, category, amount, cost) VALUES ($1, $2, $3, $4, $5)`,
			id, r.region, r.category, r.amount, r.cost); err != nil {
			t.Fatalf("insert fixture sale row: %v", err)
		}
	}
}

type dispatchORMPivotFixture struct {
	e         *Engine
	slug      string
	tenantID  string
	entryList *route.RouteEntry
}

func newDispatchORMPivotFixture(t *testing.T) *dispatchORMPivotFixture {
	t.Helper()
	conn := openDispatchORMTestDB(t)
	slug := fmt.Sprintf("dispatchormpivottest%d", time.Now().UnixNano())
	createFixtureSaleSchema(t, conn, slug)

	rt, err := wasm.New(&config.Config{
		CompilationCache:            filepath.Join(t.TempDir(), "cache"),
		Environment:                 string(config.Production),
		PoolMaxMemoryByes:           1 << 20,
		DBMaxConcurrentTransactions: 10,
	}, conn, nil, nil)
	if err != nil {
		t.Fatalf("wasm.New: %v", err)
	}
	t.Cleanup(func() { _ = rt.Close(context.Background()) })

	reg := &registry.ModuleRegistry{}
	if _, err := reg.Update(map[string]*module.LoadedModule{
		"testmodule": {
			Status: module.StatusReady,
			Manifest: manifest.Manifest{
				Name: "testmodule", Type: "standard",
				Permissions: []manifest.Permission{{Name: "testmodule:sale:cost_read"}},
			},
			ModelDecls:   []model.ModelDeclaration{pivotFixtureModelDecl()},
			Capabilities: abi.CapDBRead,
		},
	}); err != nil {
		t.Fatalf("registry Update: %v", err)
	}

	e := &Engine{primaryDB: conn, wasmRuntime: rt, moduleRegistry: reg}

	entry := &route.RouteEntry{
		ModuleName:   "testmodule",
		PathTemplate: "/testmodule/sales/pivot",
		Manifest: route.RouteManifest{
			Auth:           "required",
			Model:          "testmodule.sale",
			CrudAction:     "pivot",
			EngineNative:   true,
			StorageBackend: "table",
		},
	}

	return &dispatchORMPivotFixture{e: e, slug: slug, tenantID: "00000000-0000-0000-0000-000000000001", entryList: entry}
}

func (f *dispatchORMPivotFixture) request(target string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, target, nil)
	ctx := withRouteResolution(r.Context(), &routeResolution{
		snap:  f.e.moduleRegistry.Snapshot(),
		entry: f.entryList,
	})
	ctx = withTenantContext(ctx, &tenantresolve.TenantContext{
		TenantID:     f.tenantID,
		Slug:         f.slug,
		Entitlements: tenantresolve.EntitlementSet{Features: map[string]bool{"module.testmodule": true}},
	})
	ctx = withAuthContext(ctx, &authcheck.AuthContext{IsAuthenticated: true, UserID: "00000000-0000-0000-0000-0000000000aa"})
	return r.WithContext(ctx)
}

func (f *dispatchORMPivotFixture) dispatch(target string) (*httptest.ResponseRecorder, map[string]any) {
	w := httptest.NewRecorder()
	f.e.dispatchORMRoute(w, f.request(target))
	var body map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	return w, body
}

// findCell locates a response cell by its exact row/column dimension
// values, comparing via JSON round-tripping since a nil dimension entry
// decodes as Go nil either way.
func findCell(t *testing.T, cells []any, row, column []any) map[string]any {
	t.Helper()
	rowJSON, _ := json.Marshal(row)
	colJSON, _ := json.Marshal(column)
	for _, c := range cells {
		cell := c.(map[string]any)
		gotRow, _ := json.Marshal(cell["row"])
		gotCol, _ := json.Marshal(cell["column"])
		if string(gotRow) == string(rowJSON) && string(gotCol) == string(colJSON) {
			return cell
		}
	}
	t.Fatalf("no cell found for row=%v column=%v among %d cells", row, column, len(cells))
	return nil
}

func TestDispatchORMRoute_Pivot_BasicRollupAndGrandTotal(t *testing.T) {
	f := newDispatchORMPivotFixture(t)

	w, body := f.dispatch("/testmodule/sales/pivot?rows=region&columns=category&values=amount:sum")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	cells, _ := body["cells"].([]any)
	if len(cells) == 0 {
		t.Fatal("expected at least one cell")
	}

	eastWidgets := findCell(t, cells, []any{"east"}, []any{"widgets"})
	if got := eastWidgets["values"].(map[string]any)["amount_sum"]; fmt.Sprint(got) != "150" {
		t.Errorf("east/widgets amount_sum = %v, want 150", got)
	}

	eastSubtotal := findCell(t, cells, []any{"east"}, []any{nil})
	if got := eastSubtotal["values"].(map[string]any)["amount_sum"]; fmt.Sprint(got) != "180" {
		t.Errorf("east row subtotal amount_sum = %v, want 180", got)
	}

	grandTotal := findCell(t, cells, []any{nil}, []any{nil})
	if got := grandTotal["values"].(map[string]any)["amount_sum"]; fmt.Sprint(got) != "450" {
		t.Errorf("grand total amount_sum = %v, want 450", got)
	}
}

func TestDispatchORMRoute_Pivot_AggregationFunctions(t *testing.T) {
	f := newDispatchORMPivotFixture(t)

	w, body := f.dispatch("/testmodule/sales/pivot?rows=region&values=amount:sum,amount:avg,amount:min,amount:max,id:count,region:count_distinct")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	cells, _ := body["cells"].([]any)
	west := findCell(t, cells, []any{"west"}, []any{})
	values := west["values"].(map[string]any)

	checks := map[string]string{
		"amount_sum":            "270",
		"amount_min":            "70",
		"amount_max":            "200",
		"id_count":              "2",
		"region_count_distinct": "1",
	}
	for key, want := range checks {
		if got := fmt.Sprint(values[key]); got != want {
			t.Errorf("west %s = %v, want %v", key, got, want)
		}
	}
	// Postgres's AVG(integer) returns numeric, which pgx's generic
	// database/sql scan hands back as a decimal string ("135.0000...") to
	// avoid float precision loss — not a bare integer literal like the
	// other aggregates above.
	var avg float64
	if _, err := fmt.Sscanf(fmt.Sprint(values["amount_avg"]), "%f", &avg); err != nil || avg != 135 {
		t.Errorf("west amount_avg = %v, want 135", values["amount_avg"])
	}
}

func TestDispatchORMRoute_Pivot_FilterQueryParamNarrowsAggregation(t *testing.T) {
	f := newDispatchORMPivotFixture(t)

	w, body := f.dispatch("/testmodule/sales/pivot?rows=region&values=amount:sum&filter[category]=gadgets")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	cells, _ := body["cells"].([]any)
	east := findCell(t, cells, []any{"east"}, []any{})
	if got := east["values"].(map[string]any)["amount_sum"]; fmt.Sprint(got) != "30" {
		t.Errorf("filtered east amount_sum = %v, want 30 (gadgets only)", got)
	}
}

func TestDispatchORMRoute_Pivot_UndeclaredFieldReturns400(t *testing.T) {
	f := newDispatchORMPivotFixture(t)

	w, _ := f.dispatch("/testmodule/sales/pivot?rows=not_a_field&values=amount:sum")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
}

func TestDispatchORMRoute_Pivot_MissingRowsAndColumnsReturns400(t *testing.T) {
	f := newDispatchORMPivotFixture(t)

	w, _ := f.dispatch("/testmodule/sales/pivot?values=amount:sum")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
}

func TestDispatchORMRoute_Pivot_UnknownAggregationReturns400(t *testing.T) {
	f := newDispatchORMPivotFixture(t)

	w, _ := f.dispatch("/testmodule/sales/pivot?rows=region&values=amount:median")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
}

func TestDispatchORMRoute_Pivot_SumOnNonNumericFieldReturns400(t *testing.T) {
	f := newDispatchORMPivotFixture(t)

	w, _ := f.dispatch("/testmodule/sales/pivot?rows=amount&values=region:sum")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
}

func TestDispatchORMRoute_Pivot_ReadDeniedFieldReturns403(t *testing.T) {
	f := newDispatchORMPivotFixture(t)

	w, body := f.dispatch("/testmodule/sales/pivot?rows=region&values=cost:sum")
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body: %s", w.Code, w.Body.String())
	}
	errObj, _ := body["error"].(map[string]any)
	if errObj["code"] != abi.ErrCodeFieldReadDenied {
		t.Errorf("error code = %v, want %v", errObj["code"], abi.ErrCodeFieldReadDenied)
	}
}
