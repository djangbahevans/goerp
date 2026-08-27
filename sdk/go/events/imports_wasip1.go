//go:build wasip1

package events

//go:wasmimport host.event emit
func hostEventEmit(ptr, size uint32) uint64

//go:wasmimport host.event emit_tx
func hostEventEmitTx(ptr, size uint32) uint64
