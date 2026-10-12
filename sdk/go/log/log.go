// Package log writes structured module logs with engine-supplied invocation
// identifiers. Logging is best effort and requires no capability. Avoid logging
// personal data or credentials; secret scrubbing is only a secondary safeguard.
package log

import (
	abi "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/sdk/go/internal/hostcall"
)

// Debug writes a message when the engine log level is debug.
// Fields are alternating string keys and values, as described by Info.
func Debug(message string, fields ...any) {
	write("debug", message, fields)
}

// Info writes an informational message with alternating string keys and values.
// Unpaired keys and pairs with non-string keys are ignored; duplicate keys use
// the last value. Error values become strings. Engine fields take precedence.
// Serialization and host failures are discarded.
func Info(message string, fields ...any) {
	write("info", message, fields)
}

// Warn writes a warning with the same field rules as Info.
func Warn(message string, fields ...any) {
	write("warn", message, fields)
}

// Error writes an error message with the same field rules as Info.
func Error(message string, fields ...any) {
	write("error", message, fields)
}

func write(level, message string, fields []any) {
	input := abi.LogInput{
		Level:   level,
		Message: message,
		Fields:  make(map[string]any, len(fields)/2),
	}

	for i := 0; i+1 < len(fields); i += 2 {
		key, ok := fields[i].(string)
		if !ok {
			continue
		}

		value := fields[i+1]
		if err, ok := value.(error); ok {
			value = err.Error()
		}
		input.Fields[key] = value
	}

	_ = hostcall.Do(hostLog, input, nil)
}
