package model

import (
	"testing"
	"time"

	"github.com/vmihailenco/msgpack/v5"
)

func TestVirtual_SetsBackend(t *testing.T) {
	d := Define("legacy.inventory_item", Virtual())
	if d.Backend != BackendVirtual {
		t.Errorf("Backend = %q, want %q", d.Backend, BackendVirtual)
	}
}

func TestTransient_SetsBackendAndTTL(t *testing.T) {
	d := Define("sales.import_wizard", Transient(30*time.Minute))
	if d.Backend != BackendTransient {
		t.Errorf("Backend = %q, want %q", d.Backend, BackendTransient)
	}
	if d.TransientTTLSeconds != 1800 {
		t.Errorf("TransientTTLSeconds = %d, want 1800", d.TransientTTLSeconds)
	}
}

func TestTransient_RoundsDownToTheSecond(t *testing.T) {
	d := Define("sales.import_wizard", Transient(1500*time.Millisecond))
	if d.TransientTTLSeconds != 1 {
		t.Errorf("TransientTTLSeconds = %d, want 1", d.TransientTTLSeconds)
	}
}

func TestModelDeclaration_NoBackendOption_LeavesZeroValue(t *testing.T) {
	d := Define("sales.order", Table("orders"))
	if d.Backend != "" {
		t.Errorf("Backend = %q, want empty (table-backed default)", d.Backend)
	}
}

func TestModelDeclaration_Backend_MsgpackRoundTrip(t *testing.T) {
	original := Define("legacy.inventory_item", Virtual()).
		Field("sku", Char())

	data, err := msgpack.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var decoded ModelDeclaration
	if err := msgpack.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if decoded.Backend != BackendVirtual {
		t.Errorf("Backend = %q, want %q", decoded.Backend, BackendVirtual)
	}
}
