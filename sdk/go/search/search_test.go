package search

import (
	"testing"

	"github.com/vmihailenco/msgpack/v5"
)

func TestQueryInput_MsgpackRoundTrip(t *testing.T) {
	in := QueryInput{
		Index: "contacts",
		Query: "acme",
		Opts:  QueryOpts{Limit: 10, Offset: 5},
	}

	raw, err := msgpack.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got QueryInput
	if err := msgpack.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Index != in.Index || got.Query != in.Query || got.Opts.Limit != in.Opts.Limit || got.Opts.Offset != in.Opts.Offset {
		t.Fatalf("got %+v, want %+v", got, in)
	}
}

func TestQueryOutput_MsgpackRoundTrip(t *testing.T) {
	out := QueryOutput{
		Hits:      []map[string]any{{"id": "1", "name": "Acme"}},
		TotalHits: 1,
	}

	raw, err := msgpack.Marshal(out)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got QueryOutput
	if err := msgpack.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.TotalHits != out.TotalHits || len(got.Hits) != len(out.Hits) {
		t.Fatalf("got %+v, want %+v", got, out)
	}
}
