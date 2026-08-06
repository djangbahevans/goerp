package engine

import (
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
