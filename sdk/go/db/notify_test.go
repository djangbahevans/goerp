package db

import (
	"testing"

	abi "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/vmihailenco/msgpack/v5"
)

// TestDbNotifyInput_MsgpackWireShape checks the actual field names
// host.db.notify sees on the wire — see TestDBExecInput_MsgpackWireShape's
// own doc comment for why a struct-literal-only test can't catch a
// typo'd msgpack tag.
func TestDbNotifyInput_MsgpackWireShape(t *testing.T) {
	in := abi.DBNotifyInput{Channel: "orders", Payload: "order-123", TxID: "tx-1"}
	data, err := msgpack.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var wire map[string]any
	if err := msgpack.Unmarshal(data, &wire); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if wire["channel"] != in.Channel {
		t.Errorf(`wire["channel"] = %v, want %q`, wire["channel"], in.Channel)
	}
	if wire["payload"] != in.Payload {
		t.Errorf(`wire["payload"] = %v, want %q`, wire["payload"], in.Payload)
	}
	if wire["tx_id"] != in.TxID {
		t.Errorf(`wire["tx_id"] = %v, want %q`, wire["tx_id"], in.TxID)
	}
}
