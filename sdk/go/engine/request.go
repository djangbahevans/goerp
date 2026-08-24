package engine

import (
	"encoding/json"
	"fmt"
	"time"
)

type Request struct {
	ID     string `msgpack:"id"`
	Method string `msgpack:"method"`
	Path   string `msgpack:"path"`

	PathParams  map[string]string `msgpack:"params"`
	QueryParams map[string]string `msgpack:"query"`
	Headers     map[string]string `msgpack:"headers"`

	Body []byte `msgpack:"body"`

	UserID     string `msgpack:"user_id"`
	TenantID   string `msgpack:"tenant_id"`
	TenantSlug string `msgpack:"tenant_slug"`

	Locale    string `msgpack:"locale"`
	Timezone  string `msgpack:"timezone"`
	Currency  string `msgpack:"currency"`
	Direction string `msgpack:"direction"`

	TraceID     string    `msgpack:"trace_id"`
	RequestedAt time.Time `msgpack:"requested_at"`
}

// RawBody returns the request body's exact, unparsed bytes — the
// accessor a handler registered with the RawBody() route option
// (route_options.go) uses instead of ParseJSON, e.g. for webhook
// signature verification that needs the original byte sequence, not a
// round-tripped re-encoding of it.
func (r *Request) RawBody() []byte {
	return r.Body
}

// ParseJSON unmarshals the request body as JSON into v — the auto-parsing
// path a handler not registered with RawBody() uses. Returns a
// descriptive error on malformed JSON rather than json.Unmarshal's own
// terser error, consistent with this package's other user-facing error
// messages.
func (r *Request) ParseJSON(v any) error {
	if err := json.Unmarshal(r.Body, v); err != nil {
		return fmt.Errorf("parse json body: %w", err)
	}
	return nil
}
