package abi

// ActivityRequest is the wire shape a handle_activity invocation carries.
type ActivityRequest struct {
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
