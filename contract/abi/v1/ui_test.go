package abi

import (
	"testing"

	"github.com/vmihailenco/msgpack/v5"
)

func TestUIPushWireShapes(t *testing.T) {
	requireKeys(t, wireKeys(t, UIPushInput{}), "channel", "instructions")
	requireKeys(t, wireKeys(t, UIPushOutput{}), "delivered_count")

	instructions, err := msgpack.Marshal([]map[string]any{{"type": "reload"}})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := msgpack.Marshal(UIPushInput{Channel: "ui:user:u1", Instructions: instructions})
	if err != nil {
		t.Fatal(err)
	}
	var input struct {
		Channel      string `msgpack:"channel"`
		Instructions []byte `msgpack:"instructions"`
	}
	if err := msgpack.Unmarshal(raw, &input); err != nil {
		t.Fatal(err)
	}
	var decoded []map[string]any
	if err := msgpack.Unmarshal(input.Instructions, &decoded); err != nil {
		t.Fatalf("instructions must be msgpack bytes: %v", err)
	}
	if input.Channel != "ui:user:u1" || len(decoded) != 1 || decoded[0]["type"] != "reload" {
		t.Fatalf("input = %+v, instructions = %+v", input, decoded)
	}
}
