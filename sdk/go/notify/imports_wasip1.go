//go:build wasip1

package notify

//go:wasmimport host.notify send
func hostNotifySend(ptr, size uint32) uint64

//go:wasmimport host.notify send_tx
func hostNotifySendTx(ptr, size uint32) uint64

//go:wasmimport host.notify send_bulk
func hostNotifySendBulk(ptr, size uint32) uint64

//go:wasmimport host.notify remove_device_token
func hostNotifyRemoveDeviceToken(ptr, size uint32) uint64

//go:wasmimport host.notify update_delivery_status
func hostNotifyUpdateDeliveryStatus(ptr, size uint32) uint64
