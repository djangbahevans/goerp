package abi

// RawMessage is an already-encoded msgpack value, written to and read from
// the wire verbatim rather than wrapped as a binary blob.
type RawMessage []byte

func (m RawMessage) MarshalMsgpack() ([]byte, error) {
	return m, nil
}

func (m *RawMessage) UnmarshalMsgpack(b []byte) error {
	*m = append(RawMessage(nil), b...)
	return nil
}

// Envelope is the wire-level discriminator every host function response
// uses: Data carries the msgpack-encoded success value when OK is true,
// Error carries the failure when it is false.
type Envelope struct {
	OK    bool       `msgpack:"ok"`
	Data  RawMessage `msgpack:"data,omitempty"`
	Error *HostError `msgpack:"error,omitempty"`
}
