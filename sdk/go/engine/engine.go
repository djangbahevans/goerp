package engine

import (
	"encoding/json/jsontext"
	"encoding/json/v2"

	abi "github.com/djangbahevans/goerp/contract/abi/v1"
)

func DispatchRequest(ptr, length uint32) uint64 {
	buf := ReadMem(ptr, length)

	var req Request
	if err := unmarshal(buf, &req); err != nil {
		return WriteResponse(&Response{
			StatusCode: 400,
			Body: map[string]any{
				"error": map[string]any{
					"code":    "engine.invalid_request",
					"message": err.Error(),
				},
			},
		})
	}

	resp := DefaultRouter.Handle(&req)
	return WriteResponse(resp)
}

// WriteResponse sends resp to the engine with its body encoded as JSON by
// the value's json tags, the same encoding Request.ParseJSON reads.
func WriteResponse(resp *Response) uint64 {
	wire, err := wireResponse(resp)
	if err != nil {
		wire, _ = wireResponse(&Response{
			StatusCode: 500,
			Body: map[string]any{
				"error": map[string]any{
					"code":    "engine.marshal_failed",
					"message": err.Error(),
				},
			},
		})
	}
	return writePacked(wire)
}

func wireResponse(resp *Response) (*abi.Response, error) {
	wire := &abi.Response{StatusCode: resp.StatusCode, Headers: resp.Headers}
	if resp.Body == nil {
		return wire, nil
	}
	body, err := json.Marshal(resp.Body, jsontext.EscapeForHTML(true), jsontext.EscapeForJS(true))
	if err != nil {
		return nil, err
	}
	wire.Body = body
	return wire, nil
}
