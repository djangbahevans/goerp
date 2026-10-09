//go:build !wasip1

package notify

func hostNotifySend(ptr, size uint32) uint64 {
	panic("sdk/go/notify: host.notify.send is only available in a wasip1 build")
}

func hostNotifySendTx(ptr, size uint32) uint64 {
	panic("sdk/go/notify: host.notify.send_tx is only available in a wasip1 build")
}

func hostNotifySendBulk(ptr, size uint32) uint64 {
	panic("sdk/go/notify: host.notify.send_bulk is only available in a wasip1 build")
}

func hostNotifyRemoveDeviceToken(ptr, size uint32) uint64 {
	panic("sdk/go/notify: host.notify.remove_device_token is only available in a wasip1 build")
}

func hostNotifyUpdateDeliveryStatus(ptr, size uint32) uint64 {
	panic("sdk/go/notify: host.notify.update_delivery_status is only available in a wasip1 build")
}
