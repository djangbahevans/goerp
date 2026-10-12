package trace

import (
	"errors"
	"testing"
)

func TestInertSpanMethods(t *testing.T) {
	for _, span := range []*Span{nil, {}} {
		span.SetAttr("key", "value")
		span.SetStatus(StatusError, "failure")
		span.RecordError(errors.New("failure"))
		span.End()
		span.End()
	}
}
