// Package crypto is sdk/go's cryptographic helpers over the host.crypto
// namespace (host-abi-reference.md §17, go-sdk-reference.md §15). Signature
// comparison happens host-side in constant time; guest code never compares
// signature bytes itself.
package crypto

import (
	"encoding/hex"

	abi "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/sdk/go/internal/hostcall"
)

func verifyHMAC(algo string, key, data, sig []byte) bool {
	var out abi.CryptoVerifyHMACOutput
	in := abi.CryptoVerifyHMACInput{Algo: algo, Key: key, Data: data, Sig: sig}
	if err := hostcall.Do(hostCryptoVerifyHMAC, in, &out); err != nil {
		return false
	}
	return out.Valid
}

func verifyHMACHex(algo string, key, data []byte, sigHex string) bool {
	sig, err := hex.DecodeString(sigHex)
	if err != nil {
		return false
	}
	return verifyHMAC(algo, key, data, sig)
}

// VerifyHMACSHA256 reports whether sig is the HMAC-SHA256 of data under key.
// Any host failure reports false.
func VerifyHMACSHA256(key, data, sig []byte) bool {
	return verifyHMAC("sha256", key, data, sig)
}

// VerifyHMACSHA256Hex is VerifyHMACSHA256 for a hex-encoded signature, the
// form providers send in a signature header. A malformed sigHex reports false.
func VerifyHMACSHA256Hex(key, data []byte, sigHex string) bool {
	return verifyHMACHex("sha256", key, data, sigHex)
}

// VerifyHMACSHA512 reports whether sig is the HMAC-SHA512 of data under key.
// Any host failure reports false.
func VerifyHMACSHA512(key, data, sig []byte) bool {
	return verifyHMAC("sha512", key, data, sig)
}

// VerifyHMACSHA512Hex is VerifyHMACSHA512 for a hex-encoded signature. A
// malformed sigHex reports false.
func VerifyHMACSHA512Hex(key, data []byte, sigHex string) bool {
	return verifyHMACHex("sha512", key, data, sigHex)
}
