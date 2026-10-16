package abi

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/vmihailenco/msgpack/v5"
)

func TestComputeResponsePreservesZeroValues(t *testing.T) {
	for _, value := range []any{"", false, int64(0), nil} {
		t.Run(fmt.Sprintf("%T_%v", value, value), func(t *testing.T) {
			encoded, err := msgpack.Marshal(ComputeResponse{Value: value})
			if err != nil {
				t.Fatal(err)
			}

			var decoded ComputeResponse
			if err := msgpack.Unmarshal(encoded, &decoded); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(decoded.Value, value) {
				t.Errorf("decoded value = %#v, want %#v", decoded.Value, value)
			}
		})
	}
}
