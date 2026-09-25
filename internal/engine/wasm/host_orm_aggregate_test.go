package wasm

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/djangbahevans/goerp/internal/engine/fieldsec"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/permission"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

// aggregateSaleModelDecl declares a numeric "amount" field, a text
// "region" field, and a read-restricted numeric "cost" field — enough to
// exercise sum/count/min/max/avg, domain filtering, and the field-security
// rejection path.
func aggregateSaleModelDecl() model.ModelDeclaration {
	return model.ModelDeclaration{
		Name:  "sale",
		Table: "sales",
		Fields: []model.NamedField{
			{Name: "id", Def: model.UUID().Required().PrimaryKey()},
			{Name: "region", Def: model.Text()},
			{Name: "amount", Def: model.Integer()},
			{Name: "cost", Def: model.Integer().
				Access(model.AccessRead("testmodule:sale:cost_read")).
				OnDeniedRead(model.Omit)},
		},
	}
}

func createAggregateSaleTable(t *testing.T, conn *sql.DB, slug string, rows [][3]any) {
	t.Helper()
	ctx := context.Background()
	schema := "tenant_" + slug

	if _, err := conn.ExecContext(ctx, `CREATE TABLE `+schema+`.sales (
		id UUID PRIMARY KEY,
		region TEXT,
		amount INTEGER,
		cost INTEGER
	)`); err != nil {
		t.Fatalf("create sales table: %v", err)
	}
	for i, row := range rows {
		id := fmt.Sprintf("00000000-0000-0000-0000-%012d", i+1)
		if _, err := conn.ExecContext(ctx, `INSERT INTO `+schema+`.sales (id, region, amount, cost) VALUES ($1, $2, $3, $4)`,
			id, row[0], row[1], row[2]); err != nil {
			t.Fatalf("insert fixture row: %v", err)
		}
	}
}

// newAggregateModuleContext builds a ModuleContext over
// aggregateSaleModelDecl, with real FieldSecurityRegistry/
// PermissionRegistry wiring — grantedPermissions is the subset of
// "testmodule:sale:cost_read" the caller satisfies (none, or that one
// name).
func newAggregateModuleContext(slug string, grantedPermissions ...string) *ModuleContext {
	decl := aggregateSaleModelDecl()

	fieldSecReg := fieldsec.New()
	fieldSecReg.Register("testmodule", []model.ModelDeclaration{decl})

	permReg := permission.NewPermissionRegistry()
	permReg.Register("testmodule", []manifest.Permission{{Name: "testmodule:sale:cost_read"}})

	var permSet permission.PermissionBitfield
	for _, name := range grantedPermissions {
		idx, ok := permReg.Index(name)
		if !ok {
			panic("newAggregateModuleContext: unregistered permission " + name)
		}
		permSet.Set(idx)
	}

	return NewModuleContext("req-1", "testmodule", "user-1", "contact-1", []string{"admin"}, permSet,
		"tenant-id-1", slug, "trace-1", abi.CapDBRead, nil,
		ModuleSnapshot{
			ModelDecls:         []model.ModelDeclaration{decl},
			FieldSecRegistry:   fieldSecReg,
			PermissionRegistry: permReg,
		})
}

func setupAggregateSaleTenant(t *testing.T, primaryDB *sql.DB, prefix string, rows [][3]any) (slug string) {
	t.Helper()
	slug = fmt.Sprintf("%s%d", prefix, time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	createAggregateSaleTable(t, primaryDB, slug, rows)
	return slug
}

var aggregateSaleRows = [][3]any{
	{"east", 100, 40},
	{"east", 50, 20},
	{"west", 200, 80},
}

func TestORMAggregate_Count_EmptyDomainCountsEveryRLSVisibleRecord(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()
	slug := setupAggregateSaleTenant(t, primaryDB, "aggcount", aggregateSaleRows)
	mc := newAggregateModuleContext(slug, "testmodule:sale:cost_read")

	out, hostErr := ORMAggregate(ctx, primaryDB, mc, abiv1.ORMAggregateInput{
		Model:  "testmodule.sale",
		Values: []abiv1.ORMAggregateValue{{Aggregation: "count"}},
	})
	if hostErr != nil {
		t.Fatalf("ORMAggregate: %+v", hostErr)
	}
	if got := fmt.Sprint(out.Values["_count"]); got != "3" {
		t.Errorf("_count = %v, want 3", out.Values["_count"])
	}
}

func TestORMAggregate_Sum_ZeroMatchesReturnsZeroNotError(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()
	slug := setupAggregateSaleTenant(t, primaryDB, "aggsumzero", aggregateSaleRows)
	mc := newAggregateModuleContext(slug, "testmodule:sale:cost_read")

	out, hostErr := ORMAggregate(ctx, primaryDB, mc, abiv1.ORMAggregateInput{
		Model:  "testmodule.sale",
		Domain: `record.region = 'nowhere'`,
		Values: []abiv1.ORMAggregateValue{{Field: "amount", Aggregation: "sum"}},
	})
	if hostErr != nil {
		t.Fatalf("ORMAggregate: %+v", hostErr)
	}
	if got := fmt.Sprint(out.Values["amount_sum"]); got != "0" {
		t.Errorf("amount_sum = %v, want 0", out.Values["amount_sum"])
	}
}

func TestORMAggregate_Sum_DomainNarrowsTheTotal(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()
	slug := setupAggregateSaleTenant(t, primaryDB, "aggsumdomain", aggregateSaleRows)
	mc := newAggregateModuleContext(slug, "testmodule:sale:cost_read")

	out, hostErr := ORMAggregate(ctx, primaryDB, mc, abiv1.ORMAggregateInput{
		Model:  "testmodule.sale",
		Domain: `record.region = 'east'`,
		Values: []abiv1.ORMAggregateValue{{Field: "amount", Aggregation: "sum"}},
	})
	if hostErr != nil {
		t.Fatalf("ORMAggregate: %+v", hostErr)
	}
	if got := fmt.Sprint(out.Values["amount_sum"]); got != "150" {
		t.Errorf("amount_sum = %v, want 150", out.Values["amount_sum"])
	}
}

func TestORMAggregate_MultipleValuesInOneCall(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()
	slug := setupAggregateSaleTenant(t, primaryDB, "aggmulti", aggregateSaleRows)
	mc := newAggregateModuleContext(slug, "testmodule:sale:cost_read")

	out, hostErr := ORMAggregate(ctx, primaryDB, mc, abiv1.ORMAggregateInput{
		Model: "testmodule.sale",
		Values: []abiv1.ORMAggregateValue{
			{Aggregation: "count"},
			{Field: "amount", Aggregation: "sum"},
			{Field: "amount", Aggregation: "min"},
			{Field: "amount", Aggregation: "max"},
		},
	})
	if hostErr != nil {
		t.Fatalf("ORMAggregate: %+v", hostErr)
	}
	checks := map[string]string{"_count": "3", "amount_sum": "350", "amount_min": "50", "amount_max": "200"}
	for key, want := range checks {
		if got := fmt.Sprint(out.Values[key]); got != want {
			t.Errorf("%s = %v, want %v", key, out.Values[key], want)
		}
	}
}

func TestORMAggregate_Avg(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()
	slug := setupAggregateSaleTenant(t, primaryDB, "aggavg", aggregateSaleRows)
	mc := newAggregateModuleContext(slug, "testmodule:sale:cost_read")

	out, hostErr := ORMAggregate(ctx, primaryDB, mc, abiv1.ORMAggregateInput{
		Model:  "testmodule.sale",
		Domain: `record.region = 'east'`,
		Values: []abiv1.ORMAggregateValue{{Field: "amount", Aggregation: "avg"}},
	})
	if hostErr != nil {
		t.Fatalf("ORMAggregate: %+v", hostErr)
	}
	var avg float64
	if _, err := fmt.Sscanf(fmt.Sprint(out.Values["amount_avg"]), "%f", &avg); err != nil || avg != 75 {
		t.Errorf("amount_avg = %v, want 75", out.Values["amount_avg"])
	}
}

func TestORMAggregate_ReadDeniedFieldReturnsFieldReadDenied(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()
	slug := setupAggregateSaleTenant(t, primaryDB, "aggdenied", aggregateSaleRows)
	mc := newAggregateModuleContext(slug) // no cost_read permission granted

	_, hostErr := ORMAggregate(ctx, primaryDB, mc, abiv1.ORMAggregateInput{
		Model:  "testmodule.sale",
		Values: []abiv1.ORMAggregateValue{{Field: "cost", Aggregation: "sum"}},
	})
	if hostErr == nil {
		t.Fatal("expected an error for a denied field")
	}
	if hostErr.Code != abiv1.ErrCodeFieldReadDenied {
		t.Errorf("error code = %v, want %v", hostErr.Code, abiv1.ErrCodeFieldReadDenied)
	}
}

func TestORMAggregate_SumOnNonNumericFieldFailsValidation(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()
	slug := setupAggregateSaleTenant(t, primaryDB, "aggnonnumeric", aggregateSaleRows)
	mc := newAggregateModuleContext(slug, "testmodule:sale:cost_read")

	_, hostErr := ORMAggregate(ctx, primaryDB, mc, abiv1.ORMAggregateInput{
		Model:  "testmodule.sale",
		Values: []abiv1.ORMAggregateValue{{Field: "region", Aggregation: "sum"}},
	})
	if hostErr == nil {
		t.Fatal("expected an error for summing a non-numeric field")
	}
	if hostErr.Code != abiv1.ErrCodeValidationFailed {
		t.Errorf("error code = %v, want %v", hostErr.Code, abiv1.ErrCodeValidationFailed)
	}
}

func TestORMAggregate_UnknownAggregationFailsValidation(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()
	slug := setupAggregateSaleTenant(t, primaryDB, "aggunknown", aggregateSaleRows)
	mc := newAggregateModuleContext(slug, "testmodule:sale:cost_read")

	_, hostErr := ORMAggregate(ctx, primaryDB, mc, abiv1.ORMAggregateInput{
		Model:  "testmodule.sale",
		Values: []abiv1.ORMAggregateValue{{Field: "amount", Aggregation: "median"}},
	})
	if hostErr == nil {
		t.Fatal("expected an error for an unknown aggregation")
	}
	if hostErr.Code != abiv1.ErrCodeValidationFailed {
		t.Errorf("error code = %v, want %v", hostErr.Code, abiv1.ErrCodeValidationFailed)
	}
}

func TestORMAggregate_NoValuesFailsValidation(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()
	slug := setupAggregateSaleTenant(t, primaryDB, "aggnovalues", aggregateSaleRows)
	mc := newAggregateModuleContext(slug, "testmodule:sale:cost_read")

	_, hostErr := ORMAggregate(ctx, primaryDB, mc, abiv1.ORMAggregateInput{Model: "testmodule.sale"})
	if hostErr == nil {
		t.Fatal("expected an error for no values entries")
	}
	if hostErr.Code != abiv1.ErrCodeValidationFailed {
		t.Errorf("error code = %v, want %v", hostErr.Code, abiv1.ErrCodeValidationFailed)
	}
}

// TestORMAggregate_TxID_SeesUncommittedWriteInSameTransaction is
// host.orm.aggregate's counterpart to TestHostORM_Search_TxID_SeesUncommittedWriteInSameTransaction
// — CountTx must read within the caller's own open transaction, seeing a
// write no other connection (primaryDB itself, queried directly) can see
// yet.
func TestORMAggregate_TxID_SeesUncommittedWriteInSameTransaction(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()
	slug := setupAggregateSaleTenant(t, primaryDB, "aggtx", aggregateSaleRows)
	mc := newAggregateModuleContext(slug, "testmodule:sale:cost_read")

	const txID = "test-tx-aggregate-1"
	tx := registerTenantScopedTestTx(t, ctx, primaryDB, mc, txID)
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, "INSERT INTO sales (id, region, amount, cost) VALUES ($1, 'north', 999, 1)",
		"00000000-0000-0000-0000-000000000099"); err != nil {
		t.Fatalf("insert within tx: %v", err)
	}

	out, hostErr := ORMAggregate(ctx, primaryDB, mc, abiv1.ORMAggregateInput{
		Model:  "testmodule.sale",
		TxID:   txID,
		Values: []abiv1.ORMAggregateValue{{Aggregation: "count"}},
	})
	if hostErr != nil {
		t.Fatalf("ORMAggregate: %+v", hostErr)
	}
	if got := fmt.Sprint(out.Values["_count"]); got != "4" {
		t.Errorf("in-tx _count = %v, want 4 (3 committed + 1 uncommitted)", out.Values["_count"])
	}

	var committedCount int
	if err := primaryDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM tenant_"+slug+".sales").Scan(&committedCount); err != nil {
		t.Fatalf("count via primaryDB: %v", err)
	}
	if committedCount != 3 {
		t.Errorf("committed count seen outside the tx = %d, want 3 (uncommitted insert must not be visible)", committedCount)
	}
}
