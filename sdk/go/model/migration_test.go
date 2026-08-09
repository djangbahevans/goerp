package model

import (
	"testing"

	"github.com/vmihailenco/msgpack/v5"
)

func TestDataMigration_MsgpackRoundTrip(t *testing.T) {
	original := DataMigration{
		FromVersion: "< 1.4.0",
		ToVersion:   ">= 1.4.0",
		Description: "Backfill display_name from name",
		Handler:     "backfill_display_name",
	}

	data, err := msgpack.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var decoded DataMigration
	if err := msgpack.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if decoded != original {
		t.Fatalf("round-trip mismatch: got %+v, want %+v", decoded, original)
	}
}
