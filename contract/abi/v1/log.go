package abi

// LogInput writes a module log at debug, info, warn or error level. Fields retain
// their MessagePack types; engine identity and envelope fields take precedence.
type LogInput struct {
	Level   string         `msgpack:"level"`
	Message string         `msgpack:"message"`
	Fields  map[string]any `msgpack:"fields,omitempty"`
}

// ErrCodeLogInvalidLevel rejects a level other than debug, info, warn or error.
const ErrCodeLogInvalidLevel = "log.invalid_level"
