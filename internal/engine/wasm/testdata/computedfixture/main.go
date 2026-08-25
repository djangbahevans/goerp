// Command computedfixture is a real Go module compiled to wasip1 WASM for
// internal/engine/wasm's own InvokeHandleComputed/InvokeHandlePreview
// tests — it registers real compute functions via the SDK's
// orm.RegisterComputed and a preview hook via orm.RegisterPreviewHook,
// exporting handle_orm_compute/handle_orm_preview via
// orm.DispatchComputed/orm.DispatchPreview, the same way a real module
// would (go-sdk-reference.md §22 "Computed field recomputation"/"Preview
// action"), rather than a hand-assembled bytecode stand-in.
//
// Must be built with:
//
//	GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o computedfixture.wasm .
package main

import (
	"github.com/djangbahevans/goerp/sdk/go/engine"
	"github.com/djangbahevans/goerp/sdk/go/orm"
)

func init() {
	orm.RegisterComputed("_compute_amount_total", func(ctx orm.ComputeContext, record map[string]any) (any, error) {
		return asInt64(record["quantity"]) * asInt64(record["unit_price"]), nil
	})
	orm.RegisterComputed("_compute_hop_marker", func(ctx orm.ComputeContext, record map[string]any) (any, error) {
		return int64(1), nil
	})
	orm.RegisterPreviewHook("testmodule.priced_order", func(ctx orm.PreviewContext, draft map[string]any) map[string]any {
		draft["price_list_id"] = "list-" + ctx.TenantID
		return draft
	})
}

// asInt64 normalizes a msgpack-decoded numeric value to int64 — the wire
// type varies with the value's own magnitude (small values decode as
// int8, larger ones as int64) rather than the sender's original Go type,
// so a real caller's record (Postgres-scanned int64 columns) and a
// hand-built test payload (small literal ints) both need to be handled.
func asInt64(v any) int64 {
	switch n := v.(type) {
	case int8:
		return int64(n)
	case int16:
		return int64(n)
	case int32:
		return int64(n)
	case int64:
		return n
	case int:
		return int64(n)
	default:
		return 0
	}
}

//go:wasmexport handle_orm_compute
func handleOrmCompute(ptr, length uint32) uint64 {
	return orm.DispatchComputed(ptr, length)
}

//go:wasmexport handle_orm_preview
func handleOrmPreview(ptr, length uint32) uint64 {
	return orm.DispatchPreview(ptr, length)
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
