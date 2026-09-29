//go:build !wasip1

// Non-wasip1 builds back the host.notify imports with panicking stubs —
// see sdk/go/db/imports_stub.go's doc comment for why there's no
// meaningful mock here.
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
