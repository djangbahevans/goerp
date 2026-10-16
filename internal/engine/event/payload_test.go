package event

import (
	"fmt"
	"testing"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
)

func TestValidatePayload(t *testing.T) {
	for _, size := range []int{0, MaxPayloadBytes - 1, MaxPayloadBytes, MaxPayloadBytes + 1} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			err := ValidatePayload(make([]byte, size))
			if size <= MaxPayloadBytes {
				if err != nil {
					t.Fatal(err)
				}

				return
			}

			if err == nil || err.Code != abiv1.ErrCodeEventPayloadTooLarge || err.Retry {
				t.Fatalf("oversized payload error = %+v", err)
			}
		})
	}
}
