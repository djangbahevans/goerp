package wasm

import (
	"context"
	"database/sql"
	"encoding/base64"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/djangbahevans/goerp/internal/engine/computed"
	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/sdk/go/model"
	"github.com/vmihailenco/msgpack/v5"
)

const mutateStockID = "50000000-0000-0000-0000-000000000001"

func mutateStockModelDecl() model.ModelDeclaration {
	return model.ModelDeclaration{
		Name:  "stock",
		Table: "stocks",
		Fields: []model.NamedField{
			{Name: "id", Def: model.UUID().Required().PrimaryKey()},
			{Name: "name", Def: model.Text()},
			{Name: "on_hand", Def: model.Integer()},
			{Name: "reserved", Def: model.BigInt()},
			{Name: "weight", Def: model.Float()},
			{Name: "price", Def: model.Decimal(12, 2)},
			{Name: "etag", Def: model.Text()},
		},
	}
}

func setupMutateStockTenant(t *testing.T, primaryDB *sql.DB, prefix string, onHand int) (slug string, mc *ModuleContext, tenantID string) {
	t.Helper()
	slug = fmt.Sprintf("%s%d", prefix, time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	if _, err := primaryDB.Exec(`CREATE TABLE tenant_` + slug + `.stocks (
		id UUID PRIMARY KEY,
		name TEXT,
		on_hand INTEGER,
		reserved BIGINT,
		weight DOUBLE PRECISION,
		price NUMERIC(12,2),
		etag TEXT NOT NULL DEFAULT ''
	)`); err != nil {
		t.Fatalf("create stocks table: %v", err)
	}
	if _, err := primaryDB.Exec(`INSERT INTO tenant_`+slug+`.stocks (id, name, on_hand, reserved, weight, price) VALUES ($1, 'Bolt', $2, 0, 1.5, 10.00)`, mutateStockID, onHand); err != nil {
		t.Fatalf("seed stock: %v", err)
	}
	mc, tenantID = newORMWriteTestModuleContext(slug, []model.ModelDeclaration{mutateStockModelDecl()})
	return slug, mc, tenantID
}

func mutateOps(ops ...abiMutateOp) []abiMutateOp { return ops }

type abiMutateOp = abiv1.ORMMutateOp

func storedOnHand(t *testing.T, primaryDB *sql.DB, slug string) int {
	t.Helper()
	var n int
	if err := primaryDB.QueryRow(`SELECT on_hand FROM tenant_` + slug + `.stocks WHERE id = '` + mutateStockID + `'`).Scan(&n); err != nil {
		t.Fatalf("read on_hand: %v", err)
	}
	return n
}

func TestORMMutate_DecrementWithGuard_AppliesAndRotatesEtag(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()
	slug, mc, _ := setupMutateStockTenant(t, primaryDB, "mutateok", 10)
	r := newHostDBTestRuntime(t, primaryDB, 10)

	out, hostErr := ORMMutate(ctx, r, primaryDB, r.EventInsertClient(), mc, ORMMutateInput{
		Model: "testmodule.stock",
		ID:    mutateStockID,
		Ops:   mutateOps(abiMutateOp{Field: "on_hand", Delta: int64(-4)}, abiMutateOp{Field: "reserved", Delta: int64(4)}),
		Guard: "record.on_hand >= 4",
	})
	if hostErr != nil {
		t.Fatalf("ORMMutate: %+v", hostErr)
	}
	if got := asInt(out.Record["on_hand"]); got != 6 {
		t.Errorf("returned on_hand = %v, want 6", out.Record["on_hand"])
	}
	if got := asInt(out.Record["reserved"]); got != 4 {
		t.Errorf("returned reserved = %v, want 4", out.Record["reserved"])
	}
	if etag, _ := out.Record["etag"].(string); etag == "" {
		t.Error("etag was not rotated")
	}
	if got := storedOnHand(t, primaryDB, slug); got != 6 {
		t.Errorf("stored on_hand = %d, want 6", got)
	}
}

func TestORMMutate_ChangedFieldsAreSortedRegardlessOfOpOrder(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()
	_, mc, tenantID := setupMutateStockTenant(t, primaryDB, "mutatesorted", 10)
	r := newHostDBTestRuntime(t, primaryDB, 10)

	if _, hostErr := ORMMutate(ctx, r, primaryDB, r.EventInsertClient(), mc, ORMMutateInput{
		Model: "testmodule.stock", ID: mutateStockID,
		Ops: mutateOps(abiMutateOp{Field: "reserved", Delta: int64(1)}, abiMutateOp{Field: "on_hand", Delta: int64(-1)}),
	}); hostErr != nil {
		t.Fatalf("ORMMutate: %+v", hostErr)
	}

	events := updatedEventPayloads(t, primaryDB, tenantID)
	if len(events) != 1 {
		t.Fatalf("orm.record.updated events = %d, want 1", len(events))
	}
	assertChangedFields(t, events[0].ChangedFields, "on_hand", "reserved")
}

func TestORMMutate_FloatAndDecimalFields(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()
	slug, mc, _ := setupMutateStockTenant(t, primaryDB, "mutatefloat", 10)
	r := newHostDBTestRuntime(t, primaryDB, 10)

	if _, hostErr := ORMMutate(ctx, r, primaryDB, r.EventInsertClient(), mc, ORMMutateInput{
		Model: "testmodule.stock",
		ID:    mutateStockID,
		Ops:   mutateOps(abiMutateOp{Field: "weight", Delta: 0.25}, abiMutateOp{Field: "price", Delta: int64(2)}),
	}); hostErr != nil {
		t.Fatalf("ORMMutate: %+v", hostErr)
	}

	var weight float64
	var price string
	if err := primaryDB.QueryRow(`SELECT weight, price::text FROM tenant_`+slug+`.stocks WHERE id = '`+mutateStockID+`'`).Scan(&weight, &price); err != nil {
		t.Fatalf("read row: %v", err)
	}
	if weight != 1.75 || price != "12.00" {
		t.Errorf("stored (weight, price) = (%v, %s), want (1.75, 12.00)", weight, price)
	}
}

func TestORMMutate_NullFieldCountsAsZero(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()
	slug, mc, _ := setupMutateStockTenant(t, primaryDB, "mutatenull", 10)
	r := newHostDBTestRuntime(t, primaryDB, 10)
	if _, err := primaryDB.Exec(`UPDATE tenant_` + slug + `.stocks SET reserved = NULL`); err != nil {
		t.Fatalf("null reserved: %v", err)
	}

	out, hostErr := ORMMutate(ctx, r, primaryDB, r.EventInsertClient(), mc, ORMMutateInput{
		Model: "testmodule.stock", ID: mutateStockID,
		Ops: mutateOps(abiMutateOp{Field: "reserved", Delta: int64(3)}),
	})
	if hostErr != nil {
		t.Fatalf("ORMMutate: %+v", hostErr)
	}
	if got := asInt(out.Record["reserved"]); got != 3 {
		t.Errorf("reserved = %v, want 3", out.Record["reserved"])
	}
}

func TestORMMutate_FalseGuard_FailsPreconditionAndLeavesRecordUnchanged(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()
	slug, mc, tenantID := setupMutateStockTenant(t, primaryDB, "mutateguard", 3)
	r := newHostDBTestRuntime(t, primaryDB, 10)

	_, hostErr := ORMMutate(ctx, r, primaryDB, r.EventInsertClient(), mc, ORMMutateInput{
		Model: "testmodule.stock", ID: mutateStockID,
		Ops:   mutateOps(abiMutateOp{Field: "on_hand", Delta: int64(-5)}),
		Guard: "record.on_hand >= 5",
	})
	if hostErr == nil || hostErr.Code != abi.ErrCodePreconditionFailed {
		t.Fatalf("hostErr = %+v, want %s", hostErr, abi.ErrCodePreconditionFailed)
	}
	if got := storedOnHand(t, primaryDB, slug); got != 3 {
		t.Errorf("stored on_hand = %d, want 3 (unchanged)", got)
	}
	if got := countEventDeliveryJobsByName(t, primaryDB, "orm.record.updated", tenantID); got != 0 {
		t.Errorf("orm.record.updated jobs = %d, want 0", got)
	}
}

func TestORMMutate_UnknownRecord_NotFound(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()
	_, mc, _ := setupMutateStockTenant(t, primaryDB, "mutatemissing", 3)
	r := newHostDBTestRuntime(t, primaryDB, 10)

	_, hostErr := ORMMutate(ctx, r, primaryDB, r.EventInsertClient(), mc, ORMMutateInput{
		Model: "testmodule.stock", ID: "50000000-0000-0000-0000-0000000000ff",
		Ops:   mutateOps(abiMutateOp{Field: "on_hand", Delta: int64(-1)}),
		Guard: "record.on_hand >= 1",
	})
	if hostErr == nil || hostErr.Code != abi.ErrCodeNotFound {
		t.Fatalf("hostErr = %+v, want %s", hostErr, abi.ErrCodeNotFound)
	}
}

func TestORMMutate_ConcurrentDecrements_NeverGoBelowGuard(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()
	slug, mc, tenantID := setupMutateStockTenant(t, primaryDB, "mutaterace", 10)
	r := newHostDBTestRuntime(t, primaryDB, 10)
	insertClient := r.EventInsertClient()

	const callers = 20
	var succeeded, rejected, other atomic.Int32
	var wg sync.WaitGroup
	start := make(chan struct{})
	for range callers {
		wg.Go(func() {
			<-start
			_, hostErr := ORMMutate(ctx, r, primaryDB, insertClient, mc, ORMMutateInput{
				Model: "testmodule.stock", ID: mutateStockID,
				Ops:   mutateOps(abiMutateOp{Field: "on_hand", Delta: int64(-1)}),
				Guard: "record.on_hand >= 1",
			})
			switch {
			case hostErr == nil:
				succeeded.Add(1)
			case hostErr.Code == abi.ErrCodePreconditionFailed:
				rejected.Add(1)
			default:
				other.Add(1)
				t.Errorf("unexpected error: %+v", hostErr)
			}
		})
	}
	close(start)
	wg.Wait()

	if succeeded.Load() != 10 || rejected.Load() != callers-10 || other.Load() != 0 {
		t.Errorf("succeeded/rejected/other = %d/%d/%d, want 10/%d/0", succeeded.Load(), rejected.Load(), other.Load(), callers-10)
	}
	if got := storedOnHand(t, primaryDB, slug); got != 0 {
		t.Errorf("final on_hand = %d, want 0", got)
	}
	if got := countEventDeliveryJobsByName(t, primaryDB, "orm.record.updated", tenantID); got != 10 {
		t.Errorf("orm.record.updated jobs = %d, want 10", got)
	}
}

func TestORMMutate_Validation(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()
	slug, _, _ := setupMutateStockTenant(t, primaryDB, "mutatevalid", 10)
	r := newHostDBTestRuntime(t, primaryDB, 10)

	transient := model.ModelDeclaration{
		Name:    "session_counter",
		Backend: model.BackendTransient,
		Fields: []model.NamedField{
			{Name: "id", Def: model.UUID().Required().PrimaryKey()},
			{Name: "hits", Def: model.Integer()},
		},
	}
	virtual := model.ModelDeclaration{
		Name:    "ledger_view",
		Backend: model.BackendVirtual,
		Fields: []model.NamedField{
			{Name: "id", Def: model.UUID().Required().PrimaryKey()},
			{Name: "hits", Def: model.Integer()},
		},
	}
	computedOrder := orderModelDecl()
	decls := []model.ModelDeclaration{mutateStockModelDecl(), transient, virtual, computedOrder}
	mc, _ := newORMWriteTestModuleContext(slug, decls)

	tests := []struct {
		name  string
		model string
		ops   []abiMutateOp
		guard string
	}{
		{"non-numeric field", "testmodule.stock", mutateOps(abiMutateOp{Field: "name", Delta: int64(1)}), ""},
		{"primary key", "testmodule.stock", mutateOps(abiMutateOp{Field: "id", Delta: int64(1)}), ""},
		{"computed field", "testmodule.order", mutateOps(abiMutateOp{Field: "amount_total", Delta: int64(1)}), ""},
		{"unknown field", "testmodule.stock", mutateOps(abiMutateOp{Field: "nope", Delta: int64(1)}), ""},
		{"no ops", "testmodule.stock", nil, ""},
		{"duplicate field", "testmodule.stock", mutateOps(abiMutateOp{Field: "on_hand", Delta: int64(1)}, abiMutateOp{Field: "on_hand", Delta: int64(2)}), ""},
		{"fractional delta on integer field", "testmodule.stock", mutateOps(abiMutateOp{Field: "on_hand", Delta: 1.5}), ""},
		{"delta outside int32 on integer field", "testmodule.stock", mutateOps(abiMutateOp{Field: "on_hand", Delta: int64(1) << 40}), ""},
		{"non-numeric delta", "testmodule.stock", mutateOps(abiMutateOp{Field: "on_hand", Delta: "1"}), ""},
		{"transient model", "testmodule.session_counter", mutateOps(abiMutateOp{Field: "hits", Delta: int64(1)}), ""},
		{"virtual model", "testmodule.ledger_view", mutateOps(abiMutateOp{Field: "hits", Delta: int64(1)}), ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, hostErr := ORMMutate(ctx, r, primaryDB, r.EventInsertClient(), mc, ORMMutateInput{
				Model: tt.model, ID: mutateStockID, Ops: tt.ops, Guard: tt.guard,
			})
			if hostErr == nil || hostErr.Code != abi.ErrCodeValidationFailed {
				t.Errorf("hostErr = %+v, want %s", hostErr, abi.ErrCodeValidationFailed)
			}
		})
	}

	t.Run("invalid guard", func(t *testing.T) {
		_, hostErr := ORMMutate(ctx, r, primaryDB, r.EventInsertClient(), mc, ORMMutateInput{
			Model: "testmodule.stock", ID: mutateStockID,
			Ops: mutateOps(abiMutateOp{Field: "on_hand", Delta: int64(1)}), Guard: "record.on_hand >>> 1",
		})
		if hostErr == nil || hostErr.Code != abi.ErrCodeDomainInvalid {
			t.Errorf("hostErr = %+v, want %s", hostErr, abi.ErrCodeDomainInvalid)
		}
	})

	t.Run("integer overflow", func(t *testing.T) {
		if _, err := primaryDB.Exec(`UPDATE tenant_` + slug + `.stocks SET on_hand = 2147483647`); err != nil {
			t.Fatalf("seed max int: %v", err)
		}
		_, hostErr := ORMMutate(ctx, r, primaryDB, r.EventInsertClient(), mc, ORMMutateInput{
			Model: "testmodule.stock", ID: mutateStockID,
			Ops: mutateOps(abiMutateOp{Field: "on_hand", Delta: int64(1)}),
		})
		if hostErr == nil || hostErr.Code != abi.ErrCodeValidationFailed {
			t.Errorf("hostErr = %+v, want %s", hostErr, abi.ErrCodeValidationFailed)
		}
	})
}

func TestORMMutate_FieldWriteSecurity(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("mutatefieldsec%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	createWriteFieldSecFixtureTable(t, primaryDB, slug)
	id := "11111111-1111-1111-1111-111111111111"
	if _, err := primaryDB.Exec(`INSERT INTO tenant_`+slug+`.widgets (id, name, discount_percent) VALUES ($1, 'W', 5)`, id); err != nil {
		t.Fatalf("seed widget: %v", err)
	}
	r := newHostDBTestRuntime(t, primaryDB, 10)

	t.Run("denied field is write-denied, as for orm.Write", func(t *testing.T) {
		mc := newWriteFieldSecModuleContext(slug)
		_, hostErr := ORMMutate(ctx, r, primaryDB, r.EventInsertClient(), mc, ORMMutateInput{
			Model: "testmodule.widget", ID: id,
			Ops: mutateOps(abiMutateOp{Field: "discount_percent", Delta: int64(1)}),
		})
		if hostErr == nil || hostErr.Code != abi.ErrCodeFieldWriteDenied {
			t.Fatalf("hostErr = %+v, want %s", hostErr, abi.ErrCodeFieldWriteDenied)
		}
		if hostErr.Details["field"] != "discount_percent" {
			t.Errorf("Details[field] = %v, want discount_percent", hostErr.Details["field"])
		}
		_, writeErr := ORMWrite(ctx, r, primaryDB, r.EventInsertClient(), nil, mc, ORMWriteInput{
			Model: "testmodule.widget", ID: id, Record: map[string]any{"discount_percent": int64(6)},
		})
		if writeErr == nil || writeErr.Code != hostErr.Code {
			t.Errorf("ORMWrite error = %+v, want the same code as ORMMutate (%s)", writeErr, hostErr.Code)
		}
		var stored int
		if err := primaryDB.QueryRow(`SELECT discount_percent FROM tenant_` + slug + `.widgets WHERE id = '` + id + `'`).Scan(&stored); err != nil {
			t.Fatalf("read discount: %v", err)
		}
		if stored != 5 {
			t.Errorf("stored discount_percent = %d, want 5 (unchanged)", stored)
		}
	})

	t.Run("granted permission writes", func(t *testing.T) {
		mc := newWriteFieldSecModuleContext(slug, "sales:order:set_discount")
		out, hostErr := ORMMutate(ctx, r, primaryDB, r.EventInsertClient(), mc, ORMMutateInput{
			Model: "testmodule.widget", ID: id,
			Ops: mutateOps(abiMutateOp{Field: "discount_percent", Delta: int64(1)}),
		})
		if hostErr != nil {
			t.Fatalf("ORMMutate: %+v", hostErr)
		}
		if got := asInt(out.Record["discount_percent"]); got != 6 {
			t.Errorf("discount_percent = %v, want 6", out.Record["discount_percent"])
		}
	})
}

func TestPlanMutation_ReadonlyNumericField_NotWritable(t *testing.T) {
	decl := model.ModelDeclaration{
		Name: "counter",
		Fields: []model.NamedField{
			{Name: "id", Def: model.UUID().Required().PrimaryKey()},
			{Name: "version", Def: model.Integer().Readonly()},
		},
	}
	mc, _ := newORMWriteTestModuleContext("planreadonly", []model.ModelDeclaration{decl})

	_, hostErr := planMutation(mc, "testmodule.counter", decl, []abiMutateOp{{Field: "version", Delta: int64(1)}})
	if hostErr == nil || hostErr.Code != abi.ErrCodeFieldNotWritable {
		t.Fatalf("hostErr = %+v, want %s", hostErr, abi.ErrCodeFieldNotWritable)
	}
}

func TestORMMutate_ReturnMaskedAuditedAndEventEmitted(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	existingID := "60000000-0000-0000-0000-000000000001"
	slug := setupMaskedWriteTenant(t, primaryDB, "mutatemasked", existingID)
	mc := newMaskedWriteModuleContext(slug)
	r := newHostDBTestRuntime(t, primaryDB, 10)

	out, hostErr := ORMMutate(ctx, r, primaryDB, r.EventInsertClient(), mc, ORMMutateInput{
		Model: "testmodule.widget", ID: existingID,
		Ops: mutateOps(abiMutateOp{Field: "credit_limit", Delta: int64(-1000)}),
	})
	if hostErr != nil {
		t.Fatalf("ORMMutate: %+v", hostErr)
	}
	assertWidgetMaskedForDeniedCaller(t, out.Record, "****7890")

	rows := queryAuditLogRows(t, primaryDB, slug, "widgets")
	if len(rows) != 1 || rows[0].Operation != "UPDATE" {
		t.Fatalf("audit rows = %+v, want exactly one UPDATE", rows)
	}
	if !jsonContains(rows[0].OldData.String, `"credit_limit":5000`) || !jsonContains(rows[0].NewData.String, `"credit_limit":4000`) {
		t.Errorf("audit old/new = %s / %s, want credit_limit 5000 -> 4000", rows[0].OldData.String, rows[0].NewData.String)
	}

	if got := countEventDeliveryJobsByName(t, primaryDB, "orm.record.updated", slug); got != 1 {
		t.Fatalf("orm.record.updated jobs = %d, want 1", got)
	}
	var b64 string
	if err := primaryDB.QueryRow(`SELECT args->>'payload' FROM river_job WHERE kind = 'event_delivery' AND args->>'event_name' = 'orm.record.updated' AND args->>'tenant_id' = $1`, slug).Scan(&b64); err != nil {
		t.Fatalf("read event: %v", err)
	}
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	var body struct {
		Record        map[string]any `msgpack:"record"`
		ChangedFields []string       `msgpack:"changed_fields"`
	}
	if err := msgpack.Unmarshal(raw, &body); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if len(body.ChangedFields) != 1 || body.ChangedFields[0] != "credit_limit" {
		t.Errorf("changed_fields = %v, want [credit_limit]", body.ChangedFields)
	}
	if got := asInt(body.Record["credit_limit"]); got != 4000 {
		t.Errorf("event record credit_limit = %v, want the unmasked 4000", body.Record["credit_limit"])
	}
}

func jsonContains(doc, fragment string) bool {
	return strings.Contains(strings.ReplaceAll(doc, " ", ""), fragment)
}

func TestORMMutate_TxID_ParticipatesInCallersTransaction(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()
	slug, mc, tenantID := setupMutateStockTenant(t, primaryDB, "mutatetx", 10)
	r := newHostDBTestRuntime(t, primaryDB, 10)
	insertClient := r.EventInsertClient()

	const txID = "mutate-tx-1"
	tx := registerTenantScopedTestTx(t, ctx, primaryDB, mc, txID)
	defer func() { _ = tx.Rollback() }()

	mutate := func(delta int64, guard string) *abi.HostError {
		_, hostErr := ORMMutate(ctx, r, primaryDB, insertClient, mc, ORMMutateInput{
			Model: "testmodule.stock", ID: mutateStockID, TxID: txID,
			Ops: mutateOps(abiMutateOp{Field: "on_hand", Delta: delta}), Guard: guard,
		})
		return hostErr
	}

	if hostErr := mutate(-4, "record.on_hand >= 4"); hostErr != nil {
		t.Fatalf("first mutate: %+v", hostErr)
	}
	if hostErr := mutate(-100, "record.on_hand >= 100"); hostErr == nil || hostErr.Code != abi.ErrCodePreconditionFailed {
		t.Fatalf("second mutate = %+v, want %s", hostErr, abi.ErrCodePreconditionFailed)
	}
	// A false guard is not a SQL error, so the caller's transaction stays usable.
	if hostErr := mutate(-1, "record.on_hand >= 1"); hostErr != nil {
		t.Fatalf("third mutate: %+v", hostErr)
	}

	if got := storedOnHand(t, primaryDB, slug); got != 10 {
		t.Errorf("on_hand visible to another connection before commit = %d, want 10", got)
	}
	if got := countEventDeliveryJobsByName(t, primaryDB, "orm.record.updated", tenantID); got != 0 {
		t.Errorf("orm.record.updated jobs before commit = %d, want 0", got)
	}

	if err := tx.Rollback(); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if got := storedOnHand(t, primaryDB, slug); got != 10 {
		t.Errorf("on_hand after rollback = %d, want 10", got)
	}
}

func TestORMMutate_RecomputesDependentsAndRunsConstraintHook(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("mutatecompute%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	createFixtureOrdersTable(t, primaryDB, slug)

	r := newComputeTestRuntime(t, primaryDB)
	decls := []model.ModelDeclaration{orderModelDecl()}
	idx := computed.New()
	idx.Register("testmodule", decls)
	target := newComputeTarget(t, ctx, r, decls)
	mc := NewModuleContext("req-1", "testmodule", "user-1", "contact-1", []string{"admin"}, nil, slug, slug, "trace-1",
		abi.CapDBRead|abi.CapDBWrite, nil, ModuleSnapshot{
			ModelDecls:     decls,
			ComputedIndex:  idx,
			ComputeTargets: map[string]ComputeTarget{"testmodule": target},
		})
	insertClient := r.EventInsertClient()

	open := "70000000-0000-0000-0000-000000000001"
	locked := "70000000-0000-0000-0000-000000000002"
	for _, o := range []struct{ id, state string }{{open, "draft"}, {locked, "locked"}} {
		if _, hostErr := ORMCreate(ctx, r, primaryDB, insertClient, nil, mc, ORMCreateInput{
			Model: "testmodule.order",
			Record: map[string]any{
				"id": o.id, "tenant_id": "00000000-0000-0000-0000-000000000001",
				"quantity": int64(3), "unit_price": int64(25), "state": o.state,
			},
		}); hostErr != nil {
			t.Fatalf("ORMCreate %s: %+v", o.state, hostErr)
		}
	}

	out, hostErr := ORMMutate(ctx, r, primaryDB, insertClient, mc, ORMMutateInput{
		Model: "testmodule.order", ID: open,
		Ops: mutateOps(abiMutateOp{Field: "quantity", Delta: int64(2)}),
	})
	if hostErr != nil {
		t.Fatalf("ORMMutate: %+v", hostErr)
	}
	if got := asInt(out.Record["amount_total"]); got != 125 {
		t.Errorf("amount_total = %v, want 125 (5 * 25)", out.Record["amount_total"])
	}

	_, hostErr = ORMMutate(ctx, r, primaryDB, insertClient, mc, ORMMutateInput{
		Model: "testmodule.order", ID: locked,
		Ops: mutateOps(abiMutateOp{Field: "quantity", Delta: int64(2)}),
	})
	if hostErr == nil || hostErr.Code != abi.ErrCodeValidationFailed {
		t.Fatalf("hostErr = %+v, want %s from the write constraint hook", hostErr, abi.ErrCodeValidationFailed)
	}
	var quantity int
	if err := primaryDB.QueryRow(`SELECT quantity FROM tenant_` + slug + `."order" WHERE id = '` + locked + `'`).Scan(&quantity); err != nil {
		t.Fatalf("read quantity: %v", err)
	}
	if quantity != 3 {
		t.Errorf("locked order quantity = %d, want 3 (rolled back)", quantity)
	}
}

func TestORMMutate_RowHiddenByRLS_NotFoundNotPreconditionFailed(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug, _, _ := setupMutateStockTenant(t, primaryDB, "mutaterls", 10)
	schema := "tenant_" + slug
	repID := "22222222-2222-2222-2222-222222222222"
	otherID := "33333333-3333-3333-3333-333333333333"
	mine, theirs := "50000000-0000-0000-0000-0000000000a1", "50000000-0000-0000-0000-0000000000a2"

	for _, stmt := range []string{
		`ALTER TABLE ` + schema + `.stocks ADD COLUMN owner_id UUID`,
		`INSERT INTO ` + schema + `.stocks (id, name, on_hand, owner_id) VALUES ('` + mine + `', 'mine', 5, '` + repID + `'), ('` + theirs + `', 'theirs', 5, '` + otherID + `')`,
		`ALTER TABLE ` + schema + `.stocks ENABLE ROW LEVEL SECURITY`,
		`CREATE POLICY stocks_own_only ON ` + schema + `.stocks USING (owner_id = current_setting('app.current_user_contact_id', true)::uuid)`,
	} {
		if _, err := primaryDB.Exec(stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	writerDB := openTestRLSWriterORM(t, primaryDB, schema, schema+".stocks")

	decl := mutateStockModelDecl()
	decl.Fields = append(decl.Fields, model.NamedField{Name: "owner_id", Def: model.UUID()})
	r := newHostDBTestRuntime(t, writerDB, 10)
	mc := NewModuleContext("req-1", "testmodule", "user-1", repID, []string{"admin"}, nil, slug, slug, "trace-1",
		abi.CapDBRead|abi.CapDBWrite, nil, ModuleSnapshot{ModelDecls: []model.ModelDeclaration{decl}})

	_, hostErr := ORMMutate(ctx, r, writerDB, r.EventInsertClient(), mc, ORMMutateInput{
		Model: "testmodule.stock", ID: theirs,
		Ops:   mutateOps(abiMutateOp{Field: "on_hand", Delta: int64(-1)}),
		Guard: "record.on_hand >= 1",
	})
	if hostErr == nil || hostErr.Code != abi.ErrCodeNotFound {
		t.Errorf("hidden row: hostErr = %+v, want %s", hostErr, abi.ErrCodeNotFound)
	}

	_, hostErr = ORMMutate(ctx, r, writerDB, r.EventInsertClient(), mc, ORMMutateInput{
		Model: "testmodule.stock", ID: mine,
		Ops:   mutateOps(abiMutateOp{Field: "on_hand", Delta: int64(-9)}),
		Guard: "record.on_hand >= 9",
	})
	if hostErr == nil || hostErr.Code != abi.ErrCodePreconditionFailed {
		t.Errorf("visible row, false guard: hostErr = %+v, want %s", hostErr, abi.ErrCodePreconditionFailed)
	}

	var onHand int
	if err := primaryDB.QueryRow(`SELECT on_hand FROM `+schema+`.stocks WHERE id = $1`, theirs).Scan(&onHand); err != nil {
		t.Fatalf("read hidden row: %v", err)
	}
	if onHand != 5 {
		t.Errorf("hidden row on_hand = %d, want 5 (unchanged)", onHand)
	}
}

func openTestRLSWriterORM(t *testing.T, adminConn *sql.DB, schemaName, table string) *sql.DB {
	t.Helper()

	const roleName = "goerp_test_rls_writer_orm"
	if _, err := adminConn.Exec(`
		DO $$
		BEGIN
			IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = '` + roleName + `') THEN
				CREATE ROLE ` + roleName + ` LOGIN PASSWORD 'dev' NOSUPERUSER NOBYPASSRLS;
			END IF;
		END
		$$;
	`); err != nil {
		t.Fatalf("create test writer role: %v", err)
	}
	for _, stmt := range []string{
		"GRANT USAGE ON SCHEMA " + schemaName + " TO " + roleName,
		"GRANT SELECT, UPDATE ON " + table + " TO " + roleName,
	} {
		if _, err := adminConn.Exec(stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}

	conn, err := db.New("postgres://" + roleName + ":dev@localhost:55432/goerp")
	if err != nil {
		t.Fatalf("connect as test writer role: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}
