package tenantimport

import (
	jsonv1 "encoding/json"
	"encoding/json/v2"
	"testing"
)

// exceedsFloat64Precision is > 2^53 — the largest integer float64 can
// represent exactly — so a value this large silently changes if it's ever
// round-tripped through float64, which is exactly what plain v2 Unmarshal
// into an any would do without numberPreservingUnmarshalers.
const exceedsFloat64Precision = "9007199254740993"

// The archive's JSONL rows are decoded with numberPreservingUnmarshalers
// specifically so sqlValue below can recover the exact int64 a bigint/id
// column needs — this is the goerp#532 behavior the ticket calls out by
// name (v1's Decoder.UseNumber(), lost by a direct v1->v2 swap).

func TestDecodeRecord_LargeIntegerFieldDecodesAsNumber(t *testing.T) {
	line := []byte(`{"model":"widget","record":{"id":` + exceedsFloat64Precision + `}}`)

	var rec exportRecord
	if err := json.Unmarshal(line, &rec, json.WithUnmarshalers(numberPreservingUnmarshalers)); err != nil {
		t.Fatalf("Unmarshal() error: %v", err)
	}

	got, ok := rec.Record["id"].(jsonv1.Number)
	if !ok {
		t.Fatalf("record[id] = %#v (%T), want jsonv1.Number", rec.Record["id"], rec.Record["id"])
	}
	if got.String() != exceedsFloat64Precision {
		t.Errorf("record[id] = %s, want %s", got.String(), exceedsFloat64Precision)
	}
}

func TestDecodeRecord_NestedLargeIntegerDecodesAsNumber(t *testing.T) {
	line := []byte(`{"model":"widget","record":{"meta":{"big":` + exceedsFloat64Precision + `},"tags":[1,` + exceedsFloat64Precision + `]}}`)

	var rec exportRecord
	if err := json.Unmarshal(line, &rec, json.WithUnmarshalers(numberPreservingUnmarshalers)); err != nil {
		t.Fatalf("Unmarshal() error: %v", err)
	}

	meta, ok := rec.Record["meta"].(map[string]any)
	if !ok {
		t.Fatalf("record[meta] = %#v, want map[string]any", rec.Record["meta"])
	}
	if got, ok := meta["big"].(jsonv1.Number); !ok || got.String() != exceedsFloat64Precision {
		t.Errorf("meta[big] = %#v, want jsonv1.Number(%s)", meta["big"], exceedsFloat64Precision)
	}

	tags, ok := rec.Record["tags"].([]any)
	if !ok || len(tags) != 2 {
		t.Fatalf("record[tags] = %#v, want a 2-element []any", rec.Record["tags"])
	}
	if got, ok := tags[1].(jsonv1.Number); !ok || got.String() != exceedsFloat64Precision {
		t.Errorf("tags[1] = %#v, want jsonv1.Number(%s)", tags[1], exceedsFloat64Precision)
	}
}

func TestSqlValue_LargeIntegerPreservesInt64Precision(t *testing.T) {
	got, err := sqlValue(jsonv1.Number(exceedsFloat64Precision))
	if err != nil {
		t.Fatalf("sqlValue() error: %v", err)
	}
	i, ok := got.(int64)
	if !ok {
		t.Fatalf("sqlValue() = %#v (%T), want int64", got, got)
	}
	if i != 9007199254740993 {
		t.Errorf("sqlValue() = %d, want 9007199254740993", i)
	}
}

func TestSqlValue_NonIntegralNumberFallsBackToFloat64(t *testing.T) {
	got, err := sqlValue(jsonv1.Number("19.99"))
	if err != nil {
		t.Fatalf("sqlValue() error: %v", err)
	}
	f, ok := got.(float64)
	if !ok || f != 19.99 {
		t.Errorf("sqlValue() = %#v, want float64(19.99)", got)
	}
}

// A JSONB column value comes back from decodeRecord as map[string]any —
// sqlValue re-marshals it to JSON text for the driver, and a nested large
// integer must still round-trip exactly through that re-encode.
func TestSqlValue_NestedObjectReencodesLargeIntegerExactly(t *testing.T) {
	nested := map[string]any{"big": jsonv1.Number(exceedsFloat64Precision)}
	got, err := sqlValue(nested)
	if err != nil {
		t.Fatalf("sqlValue() error: %v", err)
	}
	s, ok := got.(string)
	if !ok {
		t.Fatalf("sqlValue() = %#v (%T), want string", got, got)
	}
	want := `{"big":` + exceedsFloat64Precision + `}`
	if s != want {
		t.Errorf("sqlValue() = %s, want %s", s, want)
	}
}
