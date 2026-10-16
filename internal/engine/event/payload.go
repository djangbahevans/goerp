package event

import (
	"fmt"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
)

const MaxPayloadBytes = 1 << 20

func ValidatePayload(payload []byte) *abiv1.HostError {
	if len(payload) > MaxPayloadBytes {
		return &abiv1.HostError{
			Code:    abiv1.ErrCodeEventPayloadTooLarge,
			Message: fmt.Sprintf("event payload is %d bytes, over the %d-byte limit", len(payload), MaxPayloadBytes),
		}
	}

	return nil
}
