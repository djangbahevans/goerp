// Command tracefixture exposes the module request's trace ID in an error
// response to test propagation across the WASM boundary.
package main

import (
	"net/http"

	"github.com/djangbahevans/goerp/sdk/go/engine"
)

func init() {
	engine.GET("/trace", func(req *engine.Request) *engine.Response {
		return &engine.Response{StatusCode: http.StatusConflict, Body: map[string]any{
			"error": map[string]any{
				"code":    "tracefixture.request.seen",
				"message": "reports the request's trace_id",
				"details": map[string]string{"seen_trace_id": req.TraceID},
			},
		}}
	})
	engine.GET("/error", func(req *engine.Request) *engine.Response {
		return &engine.Response{
			StatusCode: http.StatusTeapot,
			Headers:    map[string]string{"X-Secret": "private header", "Content-Length": "999"},
			Body: map[string]any{
				"error": map[string]any{
					"code":       req.QueryParam("code"),
					"message":    "private module message",
					"details":    map[string]string{"secret": "private module detail"},
					"request_id": "forged-request",
					"trace_id":   "forged-trace",
				},
			},
		}
	})
	engine.GET("/not-found", func(req *engine.Request) *engine.Response {
		return engine.NotFound(req.QueryParam("code"), map[string]string{"id": "c1"})
	})
	engine.POST("/decode", func(req *engine.Request) *engine.Response {
		var body map[string]any
		if err := req.ParseJSON(&body); err != nil {
			return engine.BadRequest(err)
		}

		return engine.OK(body)
	})
	engine.GET("/marshal", func(*engine.Request) *engine.Response {
		return engine.OK(make(chan int))
	})
}

//go:wasmexport get_routes
func getRoutes() uint64 {
	return engine.SerialiseRouteTable()
}

//go:wasmexport handle_request
func handleRequest(ptr, length uint32) uint64 {
	return engine.DispatchRequest(ptr, length)
}

//go:wasmexport allocate
func allocate(size uint32) uint32 {
	return engine.Allocate(size)
}

//go:wasmexport deallocate
func deallocate(ptr, size uint32) {
	engine.Deallocate(ptr, size)
}

func main() {}
