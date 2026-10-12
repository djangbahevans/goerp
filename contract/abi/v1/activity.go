package abi

// ActivityRequest carries activity identity, resolved presentation settings
// and the encoded input payload.
type ActivityRequest struct {
	Locale     string `msgpack:"locale"`
	Timezone   string `msgpack:"timezone"`
	Currency   string `msgpack:"currency"`
	Direction  string `msgpack:"direction"`
	Activity   string `msgpack:"activity"`
	Payload    []byte `msgpack:"payload"`
	TenantID   string `msgpack:"tenant_id"`
	UserID     string `msgpack:"user_id"`
	TraceID    string `msgpack:"trace_id"`
	WorkflowID string `msgpack:"workflow_id"`
	RunID      string `msgpack:"run_id"`
	Attempt    int32  `msgpack:"attempt"`
}

// ActivityResult is the wire shape a handle_activity invocation returns.
type ActivityResult struct {
	Output       []byte         `msgpack:"output,omitempty"`
	Error        string         `msgpack:"error,omitempty"`
	NonRetryable bool           `msgpack:"non_retryable"`
	ErrorType    string         `msgpack:"error_type,omitempty"`
	ErrorDetails map[string]any `msgpack:"error_details,omitempty"`
}
