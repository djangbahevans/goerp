package wasm

import (
	"context"
	"database/sql"
	"encoding/base64"
	"fmt"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/sdk/go/events"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

// rawEventPayload reads eventName's most recent EventDelivery payload for
// tenantID, undecoded.
func rawEventPayload(t *testing.T, primaryDB *sql.DB, eventName, tenantID string) []byte {
	t.Helper()
	var b64 string
	if err := primaryDB.QueryRow(
		`SELECT args->>'payload' FROM river_job
		 WHERE kind = 'event_delivery' AND args->>'event_name' = $1 AND args->>'tenant_id' = $2
		 ORDER BY id DESC LIMIT 1`, eventName, tenantID,
	).Scan(&b64); err != nil {
		t.Fatalf("read %s payload: %v", eventName, err)
	}
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	return raw
}

// TestORMRecordEvents_DecodeIntoSDKPayloadTypes decodes each real emitted
// orm.record.* event through sdk/go/events' typed payloads.
func TestORMRecordEvents_DecodeIntoSDKPayloadTypes(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("ormrecordeventssdk%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	createFixtureItemsTable(t, primaryDB, slug)

	r := newHostDBTestRuntime(t, primaryDB, 10)
	mc, tenantID := newORMWriteTestModuleContext(slug, []model.ModelDeclaration{itemModelDecl()})
	insertClient := r.EventInsertClient()

	var id1 string

	t.Run("created (single)", func(t *testing.T) {
		out, hostErr := ORMCreate(ctx, r, primaryDB, insertClient, nil, mc, ORMCreateInput{
			Model:  "testmodule.item",
			Record: map[string]any{"name": "A"},
		})
		if hostErr != nil {
			t.Fatalf("ORMCreate: %+v", hostErr)
		}
		id1, _ = out.Record["id"].(string)

		var p events.RecordCreatedPayload
		mustParsePayload(t, "orm.record.created", rawEventPayload(t, primaryDB, "orm.record.created", tenantID), &p)
		if p.Model != "testmodule.item" || p.Record["name"] != "A" || p.Records != nil {
			t.Errorf("got %+v", p)
		}
	})

	t.Run("created (batch)", func(t *testing.T) {
		out, hostErr := ORMCreateBatch(ctx, r, primaryDB, insertClient, mc, ORMCreateBatchInput{
			Model: "testmodule.item",
			Records: []map[string]any{
				{"name": "B"},
			},
		})
		if hostErr != nil {
			t.Fatalf("ORMCreateBatch: %+v", hostErr)
		}
		if len(out.Records) != 1 {
			t.Fatalf("len(Records) = %d, want 1", len(out.Records))
		}

		var p events.RecordCreatedPayload
		mustParsePayload(t, "orm.record.created", rawEventPayload(t, primaryDB, "orm.record.created", tenantID), &p)
		if p.Model != "testmodule.item" || p.Record != nil || len(p.Records) != 1 || p.Records[0]["name"] != "B" {
			t.Errorf("got %+v", p)
		}
	})

	t.Run("updated", func(t *testing.T) {
		if _, hostErr := ORMWrite(ctx, r, primaryDB, insertClient, nil, mc, ORMWriteInput{
			Model: "testmodule.item", ID: id1, Record: map[string]any{"name": "A renamed"},
		}); hostErr != nil {
			t.Fatalf("ORMWrite: %+v", hostErr)
		}

		var p events.RecordUpdatedPayload
		mustParsePayload(t, "orm.record.updated", rawEventPayload(t, primaryDB, "orm.record.updated", tenantID), &p)
		if p.Model != "testmodule.item" || p.Record["name"] != "A renamed" || len(p.ChangedFields) != 1 || p.ChangedFields[0] != "name" {
			t.Errorf("got %+v", p)
		}
	})

	t.Run("deleted", func(t *testing.T) {
		if _, hostErr := ORMUnlink(ctx, r, primaryDB, insertClient, nil, mc, ORMUnlinkInput{
			Model: "testmodule.item", IDs: []string{id1},
		}); hostErr != nil {
			t.Fatalf("ORMUnlink: %+v", hostErr)
		}

		var p events.RecordDeletedPayload
		mustParsePayload(t, "orm.record.deleted", rawEventPayload(t, primaryDB, "orm.record.deleted", tenantID), &p)
		if p.Model != "testmodule.item" || p.Record["id"] != id1 {
			t.Errorf("got %+v", p)
		}
	})
}

// mustParsePayload decodes raw through the real Event.ParsePayload path.
func mustParsePayload(t *testing.T, eventName string, raw []byte, dst any) {
	t.Helper()
	evt := events.NewEvent(eventName, eventName, 1, "", "", "", "", time.Now(), raw)
	if err := evt.ParsePayload(dst); err != nil {
		t.Fatalf("ParsePayload: %v", err)
	}
}
