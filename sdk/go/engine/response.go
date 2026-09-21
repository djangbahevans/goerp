package engine

import abi "github.com/djangbahevans/goerp/contract/abi/v1"

type Response = abi.Response

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
