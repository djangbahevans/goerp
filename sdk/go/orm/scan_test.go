package orm

import (
	"errors"
	"testing"
)

// widgetRecord is a minimal hand-written scanner, standing in for what
// goerp module generate (issue #977) will emit onto every generated
// model struct — no reflection, one type assertion per field.
type widgetRecord struct {
	ID   string
	Name string
	Note *string
}

func (w *widgetRecord) Scan(row map[string]any) error {
	if v, ok := row["id"]; ok {
		s, ok := v.(string)
		if !ok {
			return NewDecodeError("widgetRecord", "ID", "string", v)
		}
		w.ID = s
	}
	if v, ok := row["name"]; ok {
		s, ok := v.(string)
		if !ok {
			return NewDecodeError("widgetRecord", "Name", "string", v)
		}
		w.Name = s
	}
	if v, ok := row["note"]; ok && v != nil {
		s, ok := v.(string)
		if !ok {
			return NewDecodeError("widgetRecord", "Note", "*string", v)
		}
		w.Note = &s
	}
	return nil
}

func TestDecodeRecord_SuccessfulDecode(t *testing.T) {
	got, err := decodeRecord[widgetRecord](map[string]any{"id": "1", "name": "Acme"})
	if err != nil {
		t.Fatalf("decodeRecord: %v", err)
	}
	if got != (widgetRecord{ID: "1", Name: "Acme"}) {
		t.Errorf("got = %+v, want {1 Acme <nil>}", got)
	}
}

func TestDecodeRecord_TypeMismatch_ReturnsDecodeError(t *testing.T) {
	_, err := decodeRecord[widgetRecord](map[string]any{"id": "1", "name": 42})
	var decodeErr *DecodeError
	if !errors.As(err, &decodeErr) {
		t.Fatalf("decodeRecord error = %v (%T), want *DecodeError", err, err)
	}
	if decodeErr.Struct != "widgetRecord" || decodeErr.Field != "Name" || decodeErr.Expected != "string" || decodeErr.Value != 42 {
		t.Errorf("decodeErr = %+v, want {widgetRecord Name string 42}", decodeErr)
	}
}

func TestDecodeRecord_NilIntoPointerField_LeavesNil(t *testing.T) {
	got, err := decodeRecord[widgetRecord](map[string]any{"id": "1", "name": "Acme", "note": nil})
	if err != nil {
		t.Fatalf("decodeRecord: %v", err)
	}
	if got.Note != nil {
		t.Errorf("Note = %v, want nil", *got.Note)
	}
}

func TestDecodeRecord_AbsentPointerField_LeavesNil(t *testing.T) {
	got, err := decodeRecord[widgetRecord](map[string]any{"id": "1", "name": "Acme"})
	if err != nil {
		t.Fatalf("decodeRecord: %v", err)
	}
	if got.Note != nil {
		t.Errorf("Note = %v, want nil", *got.Note)
	}
}

func TestDecodeRecords_MapsEachRecord(t *testing.T) {
	recs := []map[string]any{
		{"id": "1", "name": "Acme"},
		{"id": "2", "name": "Beta"},
	}
	got, err := decodeRecords[widgetRecord](recs)
	if err != nil {
		t.Fatalf("decodeRecords: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len(got) = %d, want 2", len(got))
	}
	if got[0] != (widgetRecord{ID: "1", Name: "Acme"}) || got[1] != (widgetRecord{ID: "2", Name: "Beta"}) {
		t.Errorf("got = %+v", got)
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

// TestDecodeRecords_OneBadRecordFailsWholeBatch pins decodeRecords' own
// all-or-nothing failure semantics.
func TestDecodeRecords_OneBadRecordFailsWholeBatch(t *testing.T) {
	recs := []map[string]any{
		{"id": "1", "name": "Acme"},
		{"id": "2", "name": 42},
	}
	if _, err := decodeRecords[widgetRecord](recs); err == nil {
		t.Fatal("expected an error from the second record's mismatched name")
	}
}

func TestDecodeError_Error(t *testing.T) {
	err := NewDecodeError("Gadget", "Price", "float64", "not-a-number")
	got := err.Error()
	want := `orm: Gadget.Price: cannot assign string into float64`
	if got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}
