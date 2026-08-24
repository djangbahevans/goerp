package model

import (
	"testing"

	"github.com/vmihailenco/msgpack/v5"
)

func TestVirtual_SetsBackend(t *testing.T) {
	d := Define("legacy.inventory_item", Virtual())
	if d.Backend != BackendVirtual {
		t.Errorf("Backend = %q, want %q", d.Backend, BackendVirtual)
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
