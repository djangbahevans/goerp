//go:build !wasip1

// Non-wasip1 builds back the host.crypto import with a panicking stub — see
// sdk/go/db/imports_stub.go's doc comment for why there's no meaningful mock
// here.
package crypto

func hostCryptoVerifyHMAC(ptr, size uint32) uint64 {
	panic("sdk/go/crypto: host.crypto.verify_hmac is only available in a wasip1 build")
}
