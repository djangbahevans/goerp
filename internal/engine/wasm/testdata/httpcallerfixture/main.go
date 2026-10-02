// Command httpcallerfixture exercises the module SDK's outbound HTTP boundary.
package main

import (
	"errors"

	abi "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/sdk/go/engine"
	"github.com/djangbahevans/goerp/sdk/go/http"
	"github.com/vmihailenco/msgpack/v5"
)

//go:wasmexport run_fetch
func runFetch(ptr, size uint32) uint64 {
	var req http.FetchRequest
	env := abi.Envelope{}
	if err := msgpack.Unmarshal(engine.ReadMem(ptr, size), &req); err != nil {
		env.Error = &abi.HostError{Code: abi.ErrCodeDeserializeError, Message: err.Error()}
	} else {
		out, err := http.Fetch(req)
		if err != nil {
			if hostErr, ok := errors.AsType[*abi.HostError](err); ok {
				env.Error = hostErr
			} else {
				env.Error = &abi.HostError{Code: abi.ErrCodeUnavailable, Message: err.Error()}
			}
		} else {
			env.OK = true
			env.Data, _ = msgpack.Marshal(out)
		}
	}

	raw, err := msgpack.Marshal(env)
	if err != nil {
		panic(err)
	}

	result := engine.Allocate(uint32(len(raw)))
	engine.WriteMem(result, raw)
	return uint64(result)<<32 | uint64(len(raw))
}

//go:wasmexport allocate
func allocate(size uint32) uint32 { return engine.Allocate(size) }

//go:wasmexport deallocate
func deallocate(ptr, size uint32) { engine.Deallocate(ptr, size) }

func main() {}
