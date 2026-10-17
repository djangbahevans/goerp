package event

import (
	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/vmihailenco/msgpack/v5"
)

// Envelope carries event identity and payload to a module subscriber.
type Envelope abiv1.EventEnvelope

func (e Envelope) Marshal() ([]byte, error) {
	return msgpack.Marshal(e)
}
