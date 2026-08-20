package engine

// ActivityContext carries the Temporal-derived metadata available to an
// activity function, alongside its own typed input (go-sdk-reference.md
// §21a).
type ActivityContext struct {
	TenantID   string
	UserID     string
	TraceID    string
	WorkflowID string
	RunID      string
	Attempt    int32
}

// NonRetryableActivityError signals Temporal that retrying an activity
// would be pointless — the workflow fails instead of the activity retrying
// per its ordinary policy. Callers distinguish it from a transient error
// via errors.AsType or errors.As.
type NonRetryableActivityError struct {
	Type    string
	Details map[string]any
}

func (e *NonRetryableActivityError) Error() string { return e.Type }

// WorkflowApplicationError wraps a non-retryable activity failure.
func WorkflowApplicationError(errType string, details map[string]any) error {
	return &NonRetryableActivityError{Type: errType, Details: details}
}

type activityHandler func(ctx *ActivityContext, payload []byte) ([]byte, error)

var activityHandlers = map[string]activityHandler{}

// OnActivity registers a typed activity handler, called in init(). The
// registered wrapper decodes payload into TInput, invokes handler, and
// encodes its TOutput — the same two-layer pattern as cache.RegisterLoader
// and engine.OnEvent.
func OnActivity[TInput, TOutput any](name string, handler func(*ActivityContext, TInput) (TOutput, error)) {
	activityHandlers[name] = func(ctx *ActivityContext, payload []byte) ([]byte, error) {
		var input TInput
		if err := unmarshal(payload, &input); err != nil {
			return nil, err
		}

		output, err := handler(ctx, input)
		if err != nil {
			return nil, err
		}

		return marshal(output)
	}
}
