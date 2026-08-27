//go:build !wasip1

// Non-wasip1 builds back the host.event imports with panicking stubs —
// see sdk/go/db/imports_stub.go's doc comment for why there's no
// meaningful mock here.
package events

func hostEventEmit(ptr, size uint32) uint64 {
	panic("sdk/go/events: host.event.emit is only available in a wasip1 build")
}

func hostEventEmitTx(ptr, size uint32) uint64 {
	panic("sdk/go/events: host.event.emit_tx is only available in a wasip1 build")
}
