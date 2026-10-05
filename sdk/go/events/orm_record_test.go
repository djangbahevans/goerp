package events

import (
	"testing"

	"github.com/vmihailenco/msgpack/v5"
)

func TestORMRecordPayloads_Decode(t *testing.T) {
	t.Run("created single", func(t *testing.T) {
		raw, err := msgpack.Marshal(map[string]any{
			"model": "sales.order", "record": map[string]any{"id": "ord_1", "state": "draft"},
		})
		if err != nil {
			t.Fatalf("marshal fixture: %v", err)
		}
		var p RecordCreatedPayload
		if err := msgpack.Unmarshal(raw, &p); err != nil {
			t.Fatalf("Unmarshal: %v", err)
		}
		if p.Model != "sales.order" || p.Record["id"] != "ord_1" || p.Records != nil {
			t.Errorf("got %+v", p)
		}
	})

	t.Run("created batch", func(t *testing.T) {
		raw, err := msgpack.Marshal(map[string]any{
			"model":   "sales.order",
			"records": []map[string]any{{"id": "ord_1"}, {"id": "ord_2"}},
		})
		if err != nil {
			t.Fatalf("marshal fixture: %v", err)
		}
		var p RecordCreatedPayload
		if err := msgpack.Unmarshal(raw, &p); err != nil {
			t.Fatalf("Unmarshal: %v", err)
		}
		if p.Record != nil || len(p.Records) != 2 || p.Records[1]["id"] != "ord_2" {
			t.Errorf("got %+v", p)
		}
	})

	t.Run("updated", func(t *testing.T) {
		raw, err := msgpack.Marshal(map[string]any{
			"model": "contacts", "record": map[string]any{"id": "c_1", "credit_limit": int64(4000)},
			"changed_fields": []string{"credit_limit"},
		})
		if err != nil {
			t.Fatalf("marshal fixture: %v", err)
		}
		var p RecordUpdatedPayload
		if err := msgpack.Unmarshal(raw, &p); err != nil {
			t.Fatalf("Unmarshal: %v", err)
		}
		if p.Model != "contacts" || len(p.ChangedFields) != 1 || p.ChangedFields[0] != "credit_limit" {
			t.Errorf("got %+v", p)
		}
	})

	t.Run("deleted", func(t *testing.T) {
		raw, err := msgpack.Marshal(map[string]any{
			"model": "sales.order", "record": map[string]any{"id": "ord_1"},
		})
		if err != nil {
			t.Fatalf("marshal fixture: %v", err)
		}
		var p RecordDeletedPayload
		if err := msgpack.Unmarshal(raw, &p); err != nil {
			t.Fatalf("Unmarshal: %v", err)
		}
		if p.Model != "sales.order" || p.Record["id"] != "ord_1" {
			t.Errorf("got %+v", p)
		}
	})
}

func TestRecordDefinitions(t *testing.T) {
	for _, tc := range []struct {
		name string
		got  string
		ver  int
		want string
	}{
		{"created", RecordCreated.Name(), RecordCreated.Version(), "orm.record.created"},
		{"updated", RecordUpdated.Name(), RecordUpdated.Version(), "orm.record.updated"},
		{"deleted", RecordDeleted.Name(), RecordDeleted.Version(), "orm.record.deleted"},
	} {
		if tc.got != tc.want || tc.ver != 1 {
			t.Errorf("%s: name=%q version=%d", tc.name, tc.got, tc.ver)
		}
	}
}
