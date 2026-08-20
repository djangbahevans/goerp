package engine

import "errors"

// ActivityRequest is the wire shape DispatchActivity decodes. Not yet named
// in go-sdk-reference.md — designed here alongside DispatchActivity itself;
// the engine-side activity-dispatch endpoint that will construct these
// (goerp#255) is the natural place to revise this contract if needed.
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

// ActivityResult is the wire shape DispatchActivity returns.
type ActivityResult struct {
	Output       []byte         `msgpack:"output,omitempty"`
	Error        string         `msgpack:"error,omitempty"`
	NonRetryable bool           `msgpack:"non_retryable"`
	ErrorType    string         `msgpack:"error_type,omitempty"`
	ErrorDetails map[string]any `msgpack:"error_details,omitempty"`
}

// DispatchActivity is what a module's handle_activity export calls
// (go-sdk-reference.md §21a "Required export"): decode the incoming
// ActivityRequest, invoke the registered OnActivity handler by name, and
// pack an ActivityResult.
func DispatchActivity(ptr, length uint32) uint64 {
	buf := ReadMem(ptr, length)

	var req ActivityRequest
	if err := unmarshal(buf, &req); err != nil {
		return writePacked(&ActivityResult{Error: err.Error()})
	}

	handler, ok := activityHandlers[req.Activity]
	if !ok {
		return writePacked(&ActivityResult{Error: "activity not registered: " + req.Activity})
	}

	ctx := &ActivityContext{
		TenantID:   req.TenantID,
		UserID:     req.UserID,
		TraceID:    req.TraceID,
		WorkflowID: req.WorkflowID,
		RunID:      req.RunID,
		Attempt:    req.Attempt,
	}

	output, err := handler(ctx, req.Payload)
	if err != nil {
		if nonRetryable, ok := errors.AsType[*NonRetryableActivityError](err); ok {
			return writePacked(&ActivityResult{
				Error:        err.Error(),
				NonRetryable: true,
				ErrorType:    nonRetryable.Type,
				ErrorDetails: nonRetryable.Details,
			})
		}
		return writePacked(&ActivityResult{Error: err.Error()})
	}

	return writePacked(&ActivityResult{Output: output})
}
