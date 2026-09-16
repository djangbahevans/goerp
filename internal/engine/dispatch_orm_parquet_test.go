package engine

import (
	"bytes"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/parquet-go/parquet-go"
)

func TestNormalizeParquetValue(t *testing.T) {
	when := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	cases := []struct {
		name string
		in   any
		want any
	}{
		{"nil", nil, nil},
		{"bool", true, true},
		{"int64", int64(42), int64(42)},
		{"float64", float64(1.5), float64(1.5)},
		{"string", "hi", "hi"},
		{"time", when, when},
		{"bytes", []byte("123.45"), "123.45"},
		{"map", map[string]any{"id": "1", "display_name": "Acme"}, `{"display_name":"Acme","id":"1"}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := normalizeParquetValue(c.in)
			if got != c.want {
				t.Errorf("normalizeParquetValue(%#v) = %#v, want %#v", c.in, got, c.want)
			}
		})
	}
}

func TestParquetColumns_SortedUnion(t *testing.T) {
	records := []map[string]any{
		{"b": 1, "a": 2},
		{"c": 3, "a": 4},
	}
	got := parquetColumns(records)
	want := []string{"a", "b", "c"}
	if len(got) != len(want) {
		t.Fatalf("parquetColumns = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("parquetColumns = %v, want %v", got, want)
		}
	}
}

func TestParquetNodeFor_TypesFromFirstNonNilSample(t *testing.T) {
	records := []map[string]any{
		{"maybe_null": nil, "flag": true, "count": int64(3), "amount": float64(1.5), "label": "x"},
		{"maybe_null": nil},
	}
	cases := map[string]parquet.Node{
		"flag":       parquet.Optional(parquet.Leaf(parquet.BooleanType)),
		"count":      parquet.Optional(parquet.Leaf(parquet.Int64Type)),
		"amount":     parquet.Optional(parquet.Leaf(parquet.DoubleType)),
		"label":      parquet.Optional(parquet.String()),
		"maybe_null": parquet.Optional(parquet.String()),
	}
	for col, want := range cases {
		got := parquetNodeFor(col, records)
		if got.Type().Kind() != want.Type().Kind() {
			t.Errorf("parquetNodeFor(%q).Type().Kind() = %v, want %v", col, got.Type().Kind(), want.Type().Kind())
		}
		if got.Optional() != true {
			t.Errorf("parquetNodeFor(%q).Optional() = false, want true", col)
		}
	}
}

// TestWriteParquet_RoundTrips writes a real Parquet file via writeParquet
// and reads it back with parquet-go's own Reader, verifying the decoded
// rows match what went in — not just that encoding didn't error.
func TestWriteParquet_RoundTrips(t *testing.T) {
	when := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	records := []map[string]any{
		{"name": "Widget A", "code": "W-1", "active": true, "amount": float64(10.5), "created_at": when},
		{"name": "Widget B", "code": nil, "active": false, "amount": float64(20), "created_at": when},
	}

	rec := httptest.NewRecorder()
	writeParquet(rec, records)

	if got := rec.Header().Get("Content-Type"); got != parquetContentType {
		t.Fatalf("Content-Type = %q, want %q", got, parquetContentType)
	}
	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}

	body := rec.Body.Bytes()
	reader := parquet.NewReader(bytes.NewReader(body), parquet.NewSchema("record", parquetGroup(parquetColumns(records), records)))
	defer reader.Close()

	var got []map[string]any
	for {
		row := make(map[string]any)
		if err := reader.Read(&row); err != nil {
			break
		}
		got = append(got, row)
	}

	if len(got) != len(records) {
		t.Fatalf("read back %d rows, want %d", len(got), len(records))
	}
	if got[0]["name"] != "Widget A" || got[0]["code"] != "W-1" {
		t.Errorf("row 0 = %#v", got[0])
	}
	if got[1]["code"] != nil {
		t.Errorf("row 1 code = %#v, want nil", got[1]["code"])
	}
}

func TestWriteParquet_EmptyRecords(t *testing.T) {
	rec := httptest.NewRecorder()
	writeParquet(rec, []map[string]any{})
	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	if len(rec.Body.Bytes()) == 0 {
		t.Fatalf("expected some bytes even for zero rows (a valid empty parquet file)")
	}
}
