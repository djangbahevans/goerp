// Command webhookfixture_duplicate is a real Go module compiled to wasip1 WASM
// for internal/engine/loader's own tests: its init() registers two webhook
// verifiers, which must fail the module's load.
//
// Must be built with:
//
//	GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o webhookfixture_duplicate.wasm .
package main

import (
	"net/http"

	"github.com/djangbahevans/goerp/sdk/go/engine"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

func verify([][]byte, http.Header, []byte) (bool, string, error) {
	return true, "evt", nil
}

func init() {
	engine.RegisterWebhookVerifier(verify)
	engine.RegisterWebhookVerifier(verify)
}

//go:wasmexport get_routes
func getRoutes() uint64 {
	return engine.SerialiseRouteTable()
}

//go:wasmexport get_model_declarations
func getModelDeclarations() uint64 {
	return engine.WriteModels(model.Schema{})
}

//go:wasmexport get_data_migrations
func getDataMigrations() uint64 {
	return engine.WriteDataMigrations(nil)
}

//go:wasmexport handle_webhook_verify
func handleWebhookVerify(ptr, length uint32) uint64 {
	return engine.DispatchWebhookVerify(ptr, length)
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
