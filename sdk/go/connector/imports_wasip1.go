//go:build wasip1

package connector

//go:wasmimport host.connector inbox_get
func hostConnectorInboxGet(ptr, size uint32) uint64

//go:wasmimport host.connector inbox_mark_processed
func hostConnectorInboxMarkProcessed(ptr, size uint32) uint64

//go:wasmimport host.connector inbox_mark_failed
func hostConnectorInboxMarkFailed(ptr, size uint32) uint64
