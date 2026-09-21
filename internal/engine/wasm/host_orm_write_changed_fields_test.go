package wasm

import (
	"context"
	"database/sql"
	"encoding/base64"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/vmihailenco/msgpack/v5"
)

type updatedEventPayload struct {
	Model         string         `msgpack:"model"`
	Record        map[string]any `msgpack:"record"`
	ChangedFields []string       `msgpack:"changed_fields"`
}

func updatedEventPayloads(t *testing.T, primaryDB *sql.DB, tenantID string) []updatedEventPayload {
	t.Helper()
	rows, err := primaryDB.Query(
		`SELECT args->>'payload' FROM river_job
		 WHERE kind = 'event_delivery' AND args->>'event_name' = 'orm.record.updated' AND args->>'tenant_id' = $1
		 ORDER BY id`, tenantID)
	if err != nil {
		t.Fatalf("read orm.record.updated events: %v", err)
	}
	defer rows.Close()

	var out []updatedEventPayload
	for rows.Next() {
		var b64 string
		if err := rows.Scan(&b64); err != nil {
			t.Fatalf("scan payload: %v", err)
		}
		raw, err := base64.StdEncoding.DecodeString(b64)
		if err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		var p updatedEventPayload
		if err := msgpack.Unmarshal(raw, &p); err != nil {
			t.Fatalf("unmarshal payload: %v", err)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate payloads: %v", err)
	}
	return out
}

func assertChangedFields(t *testing.T, got []string, want ...string) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Errorf("changed_fields = %v, want %v", got, want)
	}
}

func TestORMWrite_EmitsChangedFieldsWithoutEtag(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()
	slug, mc := setupMutateStockTenant(t, primaryDB, "writechanged", 10)
	r := newHostDBTestRuntime(t, primaryDB, 10)

	if _, hostErr := ORMWrite(ctx, r, primaryDB, r.EventInsertClient(), nil, mc, ORMWriteInput{
		Model: "testmodule.stock", ID: mutateStockID,
		Record: map[string]any{"name": "Nut", "on_hand": int64(7)},
	}); hostErr != nil {
		t.Fatalf("ORMWrite: %+v", hostErr)
	}

	events := updatedEventPayloads(t, primaryDB, slug)
	if len(events) != 1 {
		t.Fatalf("orm.record.updated events = %d, want 1", len(events))
	}
	assertChangedFields(t, events[0].ChangedFields, "name", "on_hand")
	if events[0].Record["name"] != "Nut" || events[0].Record["etag"] == "" {
		t.Errorf("event record = %v, want the full stored record including the rotated etag", events[0].Record)
	}
}

func TestORMWrite_IgnoredFieldIsNotListedAsChanged(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("writechangedignore%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	createWriteFieldSecFixtureTable(t, primaryDB, slug)
	id := "11111111-1111-1111-1111-111111111111"
	if _, err := primaryDB.Exec(`INSERT INTO tenant_`+slug+`.widgets (id, name, internal_flag) VALUES ($1, 'W', false)`, id); err != nil {
		t.Fatalf("seed widget: %v", err)
	}
	r := newHostDBTestRuntime(t, primaryDB, 10)
	mc := newWriteFieldSecModuleContextForTenant(slug, slug)

	if _, hostErr := ORMWrite(ctx, r, primaryDB, r.EventInsertClient(), nil, mc, ORMWriteInput{
		Model: "testmodule.widget", ID: id,
		Record: map[string]any{"name": "Renamed", "internal_flag": true},
	}); hostErr != nil {
		t.Fatalf("ORMWrite: %+v", hostErr)
	}

	events := updatedEventPayloads(t, primaryDB, slug)
	if len(events) != 1 {
		t.Fatalf("orm.record.updated events = %d, want 1", len(events))
	}
	assertChangedFields(t, events[0].ChangedFields, "name")
}

func TestORMWriteManyAndWhere_EachEventCarriesChangedFields(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()
	slug, mc := setupMutateStockTenant(t, primaryDB, "writemanychanged", 10)
	second := "50000000-0000-0000-0000-000000000002"
	if _, err := primaryDB.Exec(`INSERT INTO tenant_`+slug+`.stocks (id, name, on_hand) VALUES ($1, 'Washer', 4)`, second); err != nil {
		t.Fatalf("seed second stock: %v", err)
	}
	r := newHostDBTestRuntime(t, primaryDB, 10)
	insertClient := r.EventInsertClient()

	if _, hostErr := ORMWriteMany(ctx, r, primaryDB, insertClient, mc, ORMWriteManyInput{
		Model: "testmodule.stock", IDs: []string{mutateStockID, second},
		Record: map[string]any{"reserved": int64(1), "name": "Bulk"},
	}); hostErr != nil {
		t.Fatalf("ORMWriteMany: %+v", hostErr)
	}
	events := updatedEventPayloads(t, primaryDB, slug)
	if len(events) != 2 {
		t.Fatalf("after write_many: events = %d, want 2", len(events))
	}
	for _, e := range events {
		assertChangedFields(t, e.ChangedFields, "name", "reserved")
	}

	if _, hostErr := ORMWriteWhere(ctx, r, primaryDB, insertClient, mc, ORMWriteWhereInput{
		Model: "testmodule.stock", Domain: "record.on_hand = 4",
		Record: map[string]any{"weight": 2.5},
	}); hostErr != nil {
		t.Fatalf("ORMWriteWhere: %+v", hostErr)
	}
	events = updatedEventPayloads(t, primaryDB, slug)
	if len(events) != 3 {
		t.Fatalf("after write_where: events = %d, want 3", len(events))
	}
	assertChangedFields(t, events[2].ChangedFields, "weight")
}
