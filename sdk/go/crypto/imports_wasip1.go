//go:build wasip1

package crypto

//go:wasmimport host.crypto verify_hmac
func hostCryptoVerifyHMAC(ptr, size uint32) uint64
