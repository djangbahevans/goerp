package wasm

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/sha512"
	"fmt"
	"hash"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/vmihailenco/msgpack/v5"
)

// registerHostCrypto attaches host.crypto.verify_hmac to the runtime.
// host.crypto needs no capability, unlike most other host.* namespaces.
func registerHostCrypto(ctx context.Context, rt wazero.Runtime, r *Runtime) error {
	_, err := rt.NewHostModuleBuilder("host.crypto").
		NewFunctionBuilder().WithFunc(makeCryptoVerifyHMAC(r)).Export("verify_hmac").
		Instantiate(ctx)
	return err
}

func hmacHash(algo string) (func() hash.Hash, bool) {
	switch algo {
	case "sha256":
		return sha256.New, true
	case "sha512":
		return sha512.New, true
	default:
		return nil, false
	}
}

// verifyHMAC recomputes the HMAC of data under key and compares it with sig
// in constant time. A sig of the wrong length is simply invalid.
func verifyHMAC(newHash func() hash.Hash, key, data, sig []byte) bool {
	mac := hmac.New(newHash, key)
	mac.Write(data)
	return hmac.Equal(mac.Sum(nil), sig)
}

func makeCryptoVerifyHMAC(r *Runtime) func(ctx context.Context, m api.Module, ptr, length uint32) uint64 {
	return func(ctx context.Context, m api.Module, ptr, length uint32) uint64 {
		allocate := r.InstanceForModule(m).allocate

		inputBytes, err := abi.ReadFromModule(m.Memory(), ptr, length)
		if err != nil {
			return abi.EncodeHostError(ctx, m, allocate, abi.MemoryFault())
		}
		var input abiv1.CryptoVerifyHMACInput
		if err := msgpack.Unmarshal(inputBytes, &input); err != nil {
			return abi.EncodeHostError(ctx, m, allocate, abi.DeserializeError(err))
		}

		newHash, ok := hmacHash(input.Algo)
		if !ok {
			return abi.EncodeHostError(ctx, m, allocate, abi.DeserializeError(
				fmt.Errorf("unsupported HMAC algorithm %q; want sha256 or sha512", input.Algo)))
		}

		return abi.WriteToModule(ctx, m, allocate, abiv1.CryptoVerifyHMACOutput{
			Valid: verifyHMAC(newHash, input.Key, input.Data, input.Sig),
		})
	}
}
