//go:build !wasip1

// Non-wasip1 builds back the host.config imports with panicking stubs —
// see sdk/go/db/imports_stub.go's doc comment for why there's no
// meaningful mock here.
package config

func hostConfigGet(ptr, size uint32) uint64 {
	panic("sdk/go/config: host.config.get is only available in a wasip1 build")
}

func hostConfigSet(ptr, size uint32) uint64 {
	panic("sdk/go/config: host.config.set is only available in a wasip1 build")
}
