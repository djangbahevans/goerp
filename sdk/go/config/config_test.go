package config

import (
	"testing"

	abi "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/vmihailenco/msgpack/v5"
)

func TestConfigGetInput_MsgpackRoundTrip(t *testing.T) {
	in := abi.ConfigGetInput{Key: "contacts.default_country_code"}

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
	in := abi.ConfigSetInput{Key: "contacts.default_country_code", Value: "GH"}

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

func TestAsInt64(t *testing.T) {
	tests := []struct {
		name string
		in   any
		want int64
		ok   bool
	}{
		{"int64", int64(42), 42, true},
		{"float64", float64(42), 42, true},
		{"string", "42", 0, false},
		{"nil", nil, 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := asInt64(tt.in)
			if got != tt.want || ok != tt.ok {
				t.Errorf("asInt64(%v) = (%v, %v), want (%v, %v)", tt.in, got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestAsFloat64(t *testing.T) {
	tests := []struct {
		name string
		in   any
		want float64
		ok   bool
	}{
		{"float64", float64(3.14), 3.14, true},
		{"int64", int64(3), 3, true},
		{"string", "3.14", 0, false},
		{"nil", nil, 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := asFloat64(tt.in)
			if got != tt.want || ok != tt.ok {
				t.Errorf("asFloat64(%v) = (%v, %v), want (%v, %v)", tt.in, got, ok, tt.want, tt.ok)
			}
		})
	}
}
