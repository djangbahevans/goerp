package main

import (
	"github.com/djangbahevans/goerp/sdk/go/engine"
	"github.com/djangbahevans/goerp/sdk/go/ui"
)

func init() {
	engine.POST("/confirm", func(*engine.Request) *engine.Response {
		return engine.UIResponse(
			map[string]string{"state": "confirmed"},
			ui.Toast("Order confirmed", ui.Success, ui.WithDuration(0)),
			ui.Reload(ui.WithTarget("sales.invoice", "inv1")),
		)
	})
	engine.POST("/instructions", func(*engine.Request) *engine.Response {
		return engine.UIInstructions(ui.CloseModal(), ui.SetBadge("/sales/count", 0))
	})
	engine.POST("/plain", func(*engine.Request) *engine.Response {
		return engine.OK(map[string]string{"ui": "record field", "data": "record data"})
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
