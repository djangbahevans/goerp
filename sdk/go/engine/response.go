package engine

// Response is what a handler returns. Body is encoded as JSON by its json
// tags when the SDK hands the response to the engine; a nil Body sends no
// body.
type Response struct {
	StatusCode int
	Headers    map[string]string
	Body       any
}

func OK(body any) *Response {
	return &Response{StatusCode: 200, Body: body}
}

func Created(body any) *Response {
	return &Response{StatusCode: 201, Body: body}
}

func NoContent() *Response {
	return &Response{StatusCode: 204}
}

func notFound() *Response {
	return &Response{
		StatusCode: 404,
		Body: map[string]any{
			"error": map[string]any{
				"code":    "engine.route_not_found",
				"message": "no route matched",
			},
		},
	}
}
