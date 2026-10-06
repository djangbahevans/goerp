package config

import (
	"testing"

	abi "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/vmihailenco/msgpack/v5"
)

func TestConfigGetInput_MsgpackRoundTrip(t *testing.T) {
	in := abi.ConfigGetInput{Key: "default_country_code"}

	raw, err := msgpack.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got abi.ConfigGetInput
	if err := msgpack.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got != in {
		t.Fatalf("got %+v, want %+v", got, in)
	}
}

func TestConfigGetOutput_MsgpackRoundTrip(t *testing.T) {
	out := abi.ConfigGetOutput{Value: []any{"GHS", "USD"}, Found: true}

	raw, err := msgpack.Marshal(out)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got abi.ConfigGetOutput
	if err := msgpack.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Found != out.Found {
		t.Fatalf("Found = %v, want %v", got.Found, out.Found)
	}
}

func TestConfigSetInput_MsgpackRoundTrip(t *testing.T) {
	in := abi.ConfigSetInput{Key: "default_country_code", Value: "GH"}

	raw, err := msgpack.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got abi.ConfigSetInput
	if err := msgpack.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Key != in.Key || got.Value != in.Value {
		t.Fatalf("got %+v, want %+v", got, in)
	}
}
