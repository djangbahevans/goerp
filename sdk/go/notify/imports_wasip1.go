//go:build wasip1

package notify

//go:wasmimport host.notify send
func hostNotifySend(ptr, size uint32) uint64

//go:wasmimport host.notify send_tx
func hostNotifySendTx(ptr, size uint32) uint64

//go:wasmimport host.notify send_bulk
func hostNotifySendBulk(ptr, size uint32) uint64
