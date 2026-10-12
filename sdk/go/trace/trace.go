// Package trace creates invocation-scoped spans for module business logic.
package trace

import (
	"sync"

	abi "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/sdk/go/internal/hostcall"
)

// Attr is a scalar span attribute constructed by String, Int, Float, or Bool.
type Attr struct {
	key   string
	value any
}

type StatusCode string

const (
	StatusUnset StatusCode = "unset"
	StatusOK    StatusCode = "ok"
	StatusError StatusCode = "error"
)

// Span belongs to the invocation that creates it. Ended, inert, and nil spans
// accept method calls without sending host requests.
type Span struct {
	mu           sync.Mutex
	id           string
	status       StatusCode
	errorMessage string
}

// Start creates a child of the innermost open span in the invocation.
// Host failures return an inert span; tracing never interrupts business logic.
func Start(name string, attrs ...Attr) *Span {
	values := make(map[string]any, len(attrs))
	for _, attr := range attrs {
		values[attr.key] = attr.value
	}

	var output abi.TraceSpanStartOutput
	if err := hostcall.Do(hostTraceSpanStart, abi.TraceSpanStartInput{Name: name, Attrs: values}, &output); err != nil {
		return &Span{}
	}

	return &Span{id: output.SpanID}
}

// End sends the pending status and error, then ends the span. Repeated calls are inert.
func (s *Span) End() {
	if s == nil {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.id == "" {
		return
	}

	_ = hostcall.Do(hostTraceSpanEnd, abi.TraceSpanEndInput{
		SpanID:       s.id,
		Status:       string(s.status),
		ErrorMessage: s.errorMessage,
	}, nil)
	s.id = ""
}

// SetAttr adds or replaces a scalar attribute. Invalid values and host failures are ignored.
func (s *Span) SetAttr(key string, value any) {
	if s == nil {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.id != "" {
		_ = hostcall.Do(hostTraceSpanAttr, abi.TraceSpanAttrInput{SpanID: s.id, Key: key, Value: value}, nil)
	}
}

// RecordError records the latest non-nil error as an exception when End is called.
// It leaves the status unchanged; SetStatus marks the span as failed.
func (s *Span) RecordError(err error) {
	if s == nil || err == nil {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.id != "" {
		s.errorMessage = err.Error()
	}
}

// SetStatus queues the final status for End. StatusError also replaces the error
// message with description; a later RecordError can replace that message.
func (s *Span) SetStatus(code StatusCode, description string) {
	if s == nil {
		return
	}

	switch code {
	case StatusUnset, StatusOK, StatusError:
	default:
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.id != "" {
		s.status = code
		if code == StatusError {
			s.errorMessage = description
		}
	}
}

func String(key, value string) Attr {
	return Attr{key: key, value: value}
}

func Int(key string, value int64) Attr {
	return Attr{key: key, value: value}
}

func Float(key string, value float64) Attr {
	return Attr{key: key, value: value}
}

func Bool(key string, value bool) Attr {
	return Attr{key: key, value: value}
}
