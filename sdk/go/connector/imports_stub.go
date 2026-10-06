//go:build !wasip1

// Non-wasip1 builds back the host.connector imports with panicking stubs —
// see sdk/go/db/imports_stub.go's doc comment for why there's no meaningful
// mock here.
package connector

func hostConnectorInboxGet(ptr, size uint32) uint64 {
	panic("sdk/go/connector: host.connector.inbox_get is only available in a wasip1 build")
}

func hostConnectorInboxMarkProcessed(ptr, size uint32) uint64 {
	panic("sdk/go/connector: host.connector.inbox_mark_processed is only available in a wasip1 build")
}

func hostConnectorInboxMarkFailed(ptr, size uint32) uint64 {
	panic("sdk/go/connector: host.connector.inbox_mark_failed is only available in a wasip1 build")
}
