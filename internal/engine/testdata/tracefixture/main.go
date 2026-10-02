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
				"code":    "tracefixture.seen",
				"message": "reports the request's trace_id",
				"details": map[string]string{"seen_trace_id": req.TraceID},
			},
		}}
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
