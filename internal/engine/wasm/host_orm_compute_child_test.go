package wasm

import (
	"database/sql"
	"fmt"
	"slices"
	"testing"
	"time"
	"uuid"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/djangbahevans/goerp/internal/engine/computed"
	"github.com/djangbahevans/goerp/sdk/go/events/def"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

func TestRecomputeAfterWrite_One2ManyHopDependency_Reparent(t *testing.T) {
	conn := openTestPrimaryDB(t)
	ctx := t.Context()
	slug := fmt.Sprintf("computemove%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, conn, slug)
	createFixtureLineOrderTables(t, conn, slug)

	r := newComputeTestRuntime(t, conn)
	decls := []model.ModelDeclaration{lineOrderModelDecl(), orderLineFixtureModelDecl()}
	idx := computed.New()
	idx.Register("testmodule", decls)
	target := newComputeTarget(t, ctx, r, decls)
	mc := NewModuleContext("req", "testmodule", "user", "", nil, nil, slug, slug, "trace",
		abi.CapDBRead|abi.CapDBWrite, nil, ModuleSnapshot{
			ModelDecls: decls, ComputedIndex: idx,
			ComputeTargets: map[string]ComputeTarget{"testmodule": target},
		})
	tenantID := "00000000-0000-0000-0000-000000000001"
	source := "31000000-0000-0000-0000-000000000001"
	destination := "31000000-0000-0000-0000-000000000002"
	child := "31000000-0000-0000-0000-000000000003"
	for _, id := range []string{source, destination} {
		if _, hostErr := ORMCreate(ctx, r, conn, r.EventInsertClient(), nil, mc, abiv1.ORMCreateInput{
			Model: "testmodule.line_order", Record: map[string]any{"id": id, "tenant_id": tenantID},
		}); hostErr != nil {
			t.Fatal(hostErr)
		}
	}
	if _, hostErr := ORMCreate(ctx, r, conn, r.EventInsertClient(), nil, mc, abiv1.ORMCreateInput{
		Model: "testmodule.order_line", Record: map[string]any{"id": child, "tenant_id": tenantID, "order_id": source, "quantity": int64(5)},
	}); hostErr != nil {
		t.Fatal(hostErr)
	}

	for _, operation := range []string{"write", "write_many", "write_where"} {
		t.Run(operation, func(t *testing.T) {
			if _, err := conn.ExecContext(t.Context(), `UPDATE tenant_`+slug+`.line_order SET lines_total = NULL`); err != nil {
				t.Fatal(err)
			}
			if _, err := conn.ExecContext(t.Context(), `UPDATE tenant_`+slug+`.order_line SET order_id = $1 WHERE id = $2`, source, child); err != nil {
				t.Fatal(err)
			}

			values := map[string]any{"order_id": destination}
			var hostErr *abiv1.HostError
			switch operation {
			case "write":
				_, hostErr = ORMWrite(t.Context(), r, conn, r.EventInsertClient(), nil, mc, abiv1.ORMWriteInput{Model: "testmodule.order_line", ID: child, Record: values})
			case "write_many":
				_, hostErr = ORMWriteMany(t.Context(), r, conn, r.EventInsertClient(), mc, abiv1.ORMWriteManyInput{Model: "testmodule.order_line", IDs: []string{child}, Record: values})
			case "write_where":
				_, hostErr = ORMWriteWhere(t.Context(), r, conn, r.EventInsertClient(), mc, abiv1.ORMWriteWhereInput{Model: "testmodule.order_line", Domain: "true", Record: values})
			}
			if hostErr != nil {
				t.Fatal(hostErr)
			}
			for _, id := range []string{source, destination} {
				var total sql.NullInt64
				if err := conn.QueryRowContext(t.Context(), `SELECT lines_total FROM tenant_`+slug+`.line_order WHERE id = $1`, id).Scan(&total); err != nil {
					t.Fatal(err)
				}
				if !total.Valid || total.Int64 != 1 {
					t.Errorf("parent %s recompute marker=%+v, want 1", id, total)
				}
			}
		})
	}
}

type parentComputedUpdated struct {
	ID            string   `msgpack:"id"`
	Total         int64    `msgpack:"lines_total"`
	ChangedFields []string `msgpack:"changed_fields"`
}

func TestRecomputeAfterWrite_One2ManyHopDependency_CrossModuleLifecycle(t *testing.T) {
	conn := openTestPrimaryDB(t)
	ctx := t.Context()
	slug := fmt.Sprintf("computeparentowner%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, conn, slug)
	createFixtureLineOrderTables(t, conn, slug)

	parent := lineOrderModelDecl()
	parent.Name = "parent.line_order"
	parent.Table = "line_order"
	parent.OnUpdate(def.Define[parentComputedUpdated]("parent.line_order.updated"))
	child := orderLineFixtureModelDecl()
	child.Fields[2].Def = model.Many2One("parent.line_order")
	idx := computed.New()
	idx.Register("parent", []model.ModelDeclaration{parent})
	idx.Register("testmodule", []model.ModelDeclaration{child})

	r := newComputeTestRuntime(t, conn)
	target := newComputeTarget(t, ctx, r, []model.ModelDeclaration{parent, child})
	mc := NewModuleContext("req", "testmodule", "user", "", nil, nil, slug, slug, "trace",
		abi.CapDBRead|abi.CapDBWrite, nil, ModuleSnapshot{
			ModelDecls:     []model.ModelDeclaration{child},
			ComputedIndex:  idx,
			ComputeTargets: map[string]ComputeTarget{"parent": target},
		})
	tenantID := uuid.New().String()
	parentID := "31000000-0000-0000-0000-000000000001"
	mc.TenantID = tenantID
	if _, err := conn.ExecContext(ctx, `INSERT INTO tenant_`+slug+`.line_order(id,tenant_id) VALUES ($1,$2)`, parentID, tenantID); err != nil {
		t.Fatal(err)
	}

	created, hostErr := ORMCreate(ctx, r, conn, r.EventInsertClient(), nil, mc, abiv1.ORMCreateInput{
		Model:  "testmodule.order_line",
		Record: map[string]any{"id": "31000000-0000-0000-0000-000000000002", "tenant_id": tenantID, "order_id": parentID, "quantity": int64(5)},
	})
	if hostErr != nil {
		t.Fatal(hostErr)
	}
	var payload parentComputedUpdated
	mustParsePayload(t, "parent update", rawEventPayload(t, conn, "parent.line_order.updated", tenantID), &payload)
	if payload.ID != parentID || payload.Total != 1 || !slices.Equal(payload.ChangedFields, []string{"lines_total"}) {
		t.Errorf("parent event=%+v", payload)
	}
	var emitter string
	if err := conn.QueryRowContext(ctx, `SELECT args->>'emitter_module' FROM system.river_job WHERE kind='event_delivery' AND args->>'tenant_id'=$1 AND args->>'event_name'='parent.line_order.updated' ORDER BY id DESC LIMIT 1`, tenantID).Scan(&emitter); err != nil {
		t.Fatal(err)
	}
	if emitter != "parent" {
		t.Errorf("emitter=%s, want parent", emitter)
	}

	if _, hostErr := ORMWrite(ctx, r, conn, r.EventInsertClient(), nil, mc, abiv1.ORMWriteInput{
		Model: "testmodule.order_line", ID: created.Record["id"].(string), Record: map[string]any{"quantity": int64(6)},
	}); hostErr != nil {
		t.Fatal(hostErr)
	}
	if count := countEventDeliveryJobsByName(t, conn, "parent.line_order.updated", tenantID); count != 1 {
		t.Errorf("unchanged computed values emitted %d parent events, want 1", count)
	}
}
