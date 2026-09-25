package abi

import "time"

// Request is the wire shape a handle_request invocation carries.
type Request struct {
	ID     string `msgpack:"id"`
	Method string `msgpack:"method"`
	Path   string `msgpack:"path"`

	// Model and Action identify the engine.Action route the engine matched;
	// both are empty for a route registered by path.
	Model  string `msgpack:"model,omitempty"`
	Action string `msgpack:"action,omitempty"`

	PathParams  map[string]string `msgpack:"params"`
	QueryParams map[string]string `msgpack:"query"`
	Headers     map[string]string `msgpack:"headers"`

	// Body is the request's raw, unparsed bytes, so a handler that needs the
	// exact bytes the client sent (webhook signature verification) can read
	// them.
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

// Response is the wire shape a handle_request invocation returns. Body is
// the response body as JSON, encoded by the module; the engine validates it
// and sends it as-is. An empty Body sends no body.
type Response struct {
	StatusCode int               `msgpack:"status"`
	Headers    map[string]string `msgpack:"headers"`
	Body       []byte            `msgpack:"body"`
}
