package orm

import "testing"

type widgetRecord struct {
	ID        string `db:"id"`
	Name      string `db:"name"`
	CreatedBy string // untagged — falls back to snake_case "created_by"
	Internal  string `db:"-"`
}

func TestDecodeRecords_MapsEachRecordByDBTag(t *testing.T) {
	recs := []map[string]any{
		{"id": "1", "name": "Acme", "created_by": "alice"},
		{"id": "2", "name": "Beta", "created_by": "bob"},
	}

	got, err := decodeRecords[widgetRecord](recs)
	if err != nil {
		t.Fatalf("decodeRecords: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len(got) = %d, want 2", len(got))
	}
	if got[0] != (widgetRecord{ID: "1", Name: "Acme", CreatedBy: "alice"}) {
		t.Errorf("got[0] = %+v, want {1 Acme alice }", got[0])
	}
	if got[1] != (widgetRecord{ID: "2", Name: "Beta", CreatedBy: "bob"}) {
		t.Errorf("got[1] = %+v, want {2 Beta bob }", got[1])
	}
}

func TestDecodeRecords_SkipsDBDashTaggedField(t *testing.T) {
	got, err := decodeRecords[widgetRecord]([]map[string]any{{"id": "1", "internal": "should not map"}})
	if err != nil {
		t.Fatalf("decodeRecords: %v", err)
	}
	if got[0].Internal != "" {
		t.Errorf("Internal = %q, want empty (db:\"-\" skips it)", got[0].Internal)
	}
}

func TestDecodeRecords_EmptyReturnsNil(t *testing.T) {
	got, err := decodeRecords[widgetRecord](nil)
	if err != nil {
		t.Fatalf("decodeRecords: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("len(got) = %d, want 0", len(got))
	}
}

func TestDecodeRecords_UnknownKeyIsSkipped(t *testing.T) {
	got, err := decodeRecords[widgetRecord]([]map[string]any{{"id": "1", "unknown_key": "whatever"}})
	if err != nil {
		t.Fatalf("decodeRecords: %v", err)
	}
	if got[0].ID != "1" {
		t.Errorf("got[0].ID = %q, want %q", got[0].ID, "1")
	}
}

// TestDecodeRecords_NullIntoNonPointerField_Errors matches
// sdk/go/db/reflect_test.go's own TestScanRow_NullIntoNonPointerField_Errors.
func TestDecodeRecords_NullIntoNonPointerField_Errors(t *testing.T) {
	if _, err := decodeRecords[widgetRecord]([]map[string]any{{"id": "1", "name": nil}}); err == nil {
		t.Fatal("expected an error assigning NULL into a non-pointer field")
	}
}

// TestDecodeRecords_OneBadRecordFailsWholeBatch pins decodeRecords' own
// all-or-nothing failure semantics, matching sdk/go/db/reflect.go's own
// scanRows.
func TestDecodeRecords_OneBadRecordFailsWholeBatch(t *testing.T) {
	recs := []map[string]any{
		{"id": "1", "name": "Acme"},
		{"id": "2", "name": nil},
	}
	if _, err := decodeRecords[widgetRecord](recs); err == nil {
		t.Fatal("expected an error from the second record's NULL name")
	}
}

func TestToSnakeCase(t *testing.T) {
	tests := []struct{ in, want string }{
		{"CustomerName", "customer_name"},
		{"ID", "id"},
		{"URL", "url"},
		{"CreatedAt", "created_at"},
	}
	for _, tt := range tests {
		if got := toSnakeCase(tt.in); got != tt.want {
			t.Errorf("toSnakeCase(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
