package engine

import (
	"net/http"

	abi "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/sdk/go/ui"
)

// Response is what a handler returns. Body is encoded as JSON by its json
// tags when the SDK hands the response to the engine; a nil Body sends no
// body.
type Response struct {
	StatusCode int
	Headers    map[string]string
	Body       any
	errorKind  string
}

func OK(body any) *Response {
	return &Response{StatusCode: 200, Body: body}
}

// UIResponse returns 200 with data and an ordered UI instruction array in an
// envelope marked by X-GoERP-UI: 1. It requires no capability. Nil data is null.
func UIResponse(data any, instructions ...ui.Instruction) *Response {
	resp := UIInstructions(instructions...)
	resp.Body = struct {
		Data any              `json:"data"`
		UI   []ui.Instruction `json:"ui"`
	}{Data: data, UI: instructions}

	return resp
}

// UIInstructions returns 200 with only an ordered UI instruction array in an
// envelope marked by X-GoERP-UI: 1. It requires no capability.
func UIInstructions(instructions ...ui.Instruction) *Response {
	return &Response{
		StatusCode: http.StatusOK,
		Headers:    map[string]string{"X-GoERP-UI": "1"},
		Body: struct {
			UI []ui.Instruction `json:"ui"`
		}{UI: instructions},
	}
}

func Created(body any) *Response {
	return &Response{StatusCode: 201, Body: body}
}

func NoContent() *Response {
	return &Response{StatusCode: 204}
}

// BadRequest answers 400 with err's message.
func BadRequest(err error) *Response {
	return &Response{
		StatusCode: 400,
		errorKind:  abi.FrameworkError,
		Body: map[string]any{
			"error": map[string]any{
				"code":    "invalid_body",
				"message": err.Error(),
			},
		},
	}
}

func notFound() *Response {
	return &Response{
		StatusCode: 404,
		errorKind:  abi.FrameworkError,
		Body: map[string]any{
			"error": map[string]any{
				"code":    "engine.route_not_found",
				"message": "no route matched",
			},
		},
	}
}

// Error returns an application error with code, message and optional details.
// The code must be declared in the module's error_codes. Standard conditions
// determine the status; custom conditions use status (400–599), or 500 otherwise.
// A backend errors.{suffix} translation replaces message using the caller's locale
// and locale fallback chain; a missing translation preserves message.
// Translation preserves code, status and details.
func Error(status int, code, message string, details any) *Response {
	body := map[string]any{"code": code, "message": message}
	if details != nil {
		body["details"] = details
	}

	return &Response{
		StatusCode: abi.ApplicationErrorStatus(code, status),
		Body:       map[string]any{"error": body},
	}
}

// NotFound returns a declared application error with a default status of 404.
// Standard code conditions override that status; details may be nil.
func NotFound(code string, details any) *Response {
	return Error(http.StatusNotFound, code, http.StatusText(http.StatusNotFound), details)
}

// Conflict returns a declared application error with a default status of 409.
// Standard code conditions override that status; details may be nil.
func Conflict(code string, details any) *Response {
	return Error(http.StatusConflict, code, http.StatusText(http.StatusConflict), details)
}

// Forbidden returns a declared application error with a default status of 403.
// Standard code conditions override that status; details may be nil.
func Forbidden(code string, details any) *Response {
	return Error(http.StatusForbidden, code, http.StatusText(http.StatusForbidden), details)
}
