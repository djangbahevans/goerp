package search

import (
	"testing"

	"github.com/vmihailenco/msgpack/v5"
)

func TestSearchQueryInput_MsgpackRoundTrip(t *testing.T) {
	in := searchQueryInput{
		Index: "contacts",
		Query: "acme",
		Opts:  searchQueryOpts{Limit: 10, Offset: 5},
	}

	raw, err := msgpack.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got searchQueryInput
	if err := msgpack.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Index != in.Index || got.Query != in.Query || got.Opts.Limit != in.Opts.Limit || got.Opts.Offset != in.Opts.Offset {
		t.Fatalf("got %+v, want %+v", got, in)
	}
}

func TestSearchQueryOutput_MsgpackRoundTrip(t *testing.T) {
	out := searchQueryOutput{
		Hits:      []map[string]any{{"id": "1", "name": "Acme"}},
		TotalHits: 1,
	}

	raw, err := msgpack.Marshal(out)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got searchQueryOutput
	if err := msgpack.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.TotalHits != out.TotalHits || len(got.Hits) != len(out.Hits) {
		t.Fatalf("got %+v, want %+v", got, out)
	}
}

func TestSearchOptions_SetFields(t *testing.T) {
	var in searchQueryInput

	WithFilter("is_active = true")(&in)
	WithSort("name:asc", "created_at:desc")(&in)
	WithLimit(50)(&in)
	WithOffset(10)(&in)
	WithFacets("type", "country_code")(&in)

	if in.Opts.Filter != "is_active = true" {
		t.Errorf("Filter = %q, want %q", in.Opts.Filter, "is_active = true")
	}
	if len(in.Opts.Sort) != 2 || in.Opts.Sort[0] != "name:asc" || in.Opts.Sort[1] != "created_at:desc" {
		t.Errorf("Sort = %v, want [name:asc created_at:desc]", in.Opts.Sort)
	}
	if in.Opts.Limit != 50 {
		t.Errorf("Limit = %d, want 50", in.Opts.Limit)
	}
	if in.Opts.Offset != 10 {
		t.Errorf("Offset = %d, want 10", in.Opts.Offset)
	}
	if len(in.Opts.Facets) != 2 || in.Opts.Facets[0] != "type" || in.Opts.Facets[1] != "country_code" {
		t.Errorf("Facets = %v, want [type country_code]", in.Opts.Facets)
	}
}

type contactHit struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email,omitempty"`
}

func TestDecodeHits_MapsEachHitByJSONTag(t *testing.T) {
	hits := []map[string]any{
		{"id": "1", "name": "Acme", "email": "acme@example.com"},
		{"id": "2", "name": "Beta"},
	}

	got, err := decodeHits[contactHit](hits)
	if err != nil {
		t.Fatalf("decodeHits: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len(got) = %d, want 2", len(got))
	}
	if got[0] != (contactHit{ID: "1", Name: "Acme", Email: "acme@example.com"}) {
		t.Errorf("got[0] = %+v, want {1 Acme acme@example.com}", got[0])
	}
	if got[1] != (contactHit{ID: "2", Name: "Beta"}) {
		t.Errorf("got[1] = %+v, want {2 Beta }", got[1])
	}
}

func TestDecodeHits_EmptyHitsReturnsEmptySlice(t *testing.T) {
	got, err := decodeHits[contactHit](nil)
	if err != nil {
		t.Fatalf("decodeHits: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("len(got) = %d, want 0", len(got))
	}
}

func TestDecodeHits_UnknownKeyIsSkipped(t *testing.T) {
	got, err := decodeHits[contactHit]([]map[string]any{{"id": "1", "unknown_key": "whatever"}})
	if err != nil {
		t.Fatalf("decodeHits: %v", err)
	}
	if got[0].ID != "1" {
		t.Errorf("got[0].ID = %q, want %q", got[0].ID, "1")
	}
}

// TestDecodeHits_NullIntoNonPointerField_Errors matches
// sdk/go/db/reflect_test.go's own TestScanRow_NullIntoNonPointerField_Errors
// — a non-pointer field can't represent NULL, so it must error rather than
// silently zero-value.
func TestDecodeHits_NullIntoNonPointerField_Errors(t *testing.T) {
	if _, err := decodeHits[contactHit]([]map[string]any{{"id": "1", "name": nil}}); err == nil {
		t.Fatal("expected an error assigning NULL into a non-pointer field")
	}
}

// TestDecodeHits_OneBadHitFailsWholeBatch pins decodeHits' all-or-nothing
// failure semantics — matching sdk/go/db/reflect.go's own scanRows, which
// fails a whole []T on the first bad row rather than dropping just that
// one.
func TestDecodeHits_OneBadHitFailsWholeBatch(t *testing.T) {
	hits := []map[string]any{
		{"id": "1", "name": "Acme"},
		{"id": "2", "name": nil},
	}
	if _, err := decodeHits[contactHit](hits); err == nil {
		t.Fatal("expected an error from the second hit's NULL name")
	}
}
