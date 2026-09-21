package engine

import (
	"errors"

	abi "github.com/djangbahevans/goerp/contract/abi/v1"
)

type (
	ActivityRequest = abi.ActivityRequest
	ActivityResult  = abi.ActivityResult
)

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
