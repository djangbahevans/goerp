package engine

type Response struct {
	StatusCode int               `msgpack:"status"`
	Headers    map[string]string `msgpack:"headers"`
	Body       any               `msgpack:"body"`
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
