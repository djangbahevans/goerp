// Command actinguserfixture exposes ABAC session variables and visible records
// through the module SQL host interface.
package main

import (
	"net/http"

	"github.com/djangbahevans/goerp/sdk/go/db"
	"github.com/djangbahevans/goerp/sdk/go/engine"
)

func init() {
	engine.GET("/sql", func(req *engine.Request) *engine.Response {
		settings, err := db.QueryRaw(`SELECT current_setting('app.current_user_contact_id') AS contact_id,
			current_setting('app.current_user_roles') AS roles`, nil)
		if err != nil {
			return &engine.Response{StatusCode: http.StatusInternalServerError, Body: err.Error()}
		}
		records, err := db.QueryRaw("SELECT name FROM widget ORDER BY name", nil)
		if err != nil {
			return &engine.Response{StatusCode: http.StatusInternalServerError, Body: err.Error()}
		}
		return &engine.Response{StatusCode: http.StatusOK, Body: map[string]any{
			"settings": settings.AsMaps(), "data": records.AsMaps(),
		}}
	})
}

//go:wasmexport handle_request
func handleRequest(ptr, length uint32) uint64 { return engine.DispatchRequest(ptr, length) }

//go:wasmexport allocate
func allocate(size uint32) uint32 { return engine.Allocate(size) }

//go:wasmexport deallocate
func deallocate(ptr, size uint32) { engine.Deallocate(ptr, size) }

func main() {}
