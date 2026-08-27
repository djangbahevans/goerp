//go:build !wasip1

// Non-wasip1 builds back the host.authz import with a panicking stub —
// see sdk/go/db/imports_stub.go's doc comment for why there's no
// meaningful mock here.
package authz

func hostAuthzFieldCheck(ptr, size uint32) uint64 {
	panic("sdk/go/authz: host.authz.field_check is only available in a wasip1 build")
}
