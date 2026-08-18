package abi

import (
	"context"
	"fmt"

	"github.com/tetratelabs/wazero/api"
	"github.com/vmihailenco/msgpack/v5"
)

// envelope is the wire-level discriminator every host function response
// uses. host-abi-reference.md §2/§3 specify msgpack framing and that errors
// are "returned in place of the normal response" without naming a literal
// envelope type — this is that type, established here since host.db.begin/
// commit/rollback are the first host functions to need one.
type envelope struct {
	OK    bool               `msgpack:"ok"`
	Data  msgpack.RawMessage `msgpack:"data,omitempty"`
	Error *HostError         `msgpack:"error,omitempty"`
}

// ReadFromModule reads length bytes from the calling module's linear memory
// at ptr, bounds-checked against the module's actual memory size, and
// copies them out — the module may reuse or free that region as soon as the
// host call returns.
func ReadFromModule(mem api.Memory, ptr, length uint32) ([]byte, error) {
	data, ok := mem.Read(ptr, length)
	if !ok {
		return nil, fmt.Errorf("read out of bounds at ptr=%d len=%d", ptr, length)
	}

	out := make([]byte, len(data))
	copy(out, data)
	return out, nil
}

// WriteToModule marshals value into an envelope, allocates space for it in
// the module's own linear memory via its allocate export, writes it, and
// returns the packed ptr/len i64 (host-abi-reference.md §2 "Return value
// packing"). On allocation failure it falls back to encoding an
// abi.allocation_failed error in the same call.
func WriteToModule(ctx context.Context, mod api.Module, allocate api.Function, value any) uint64 {
	data, err := msgpack.Marshal(value)
	if err != nil {
		return EncodeHostError(ctx, mod, allocate, DeserializeError(err))
	}

	return packAndWrite(ctx, mod, allocate, envelope{OK: true, Data: data})
}

// EncodeHostError packs hostErr the same way WriteToModule packs a success
// value — same envelope, same allocate/write/pack path — since errors are
// returned in place of the normal response, not through a separate channel.
func EncodeHostError(ctx context.Context, mod api.Module, allocate api.Function, hostErr *HostError) uint64 {
	return packAndWrite(ctx, mod, allocate, envelope{OK: false, Error: hostErr})
}

func packAndWrite(ctx context.Context, mod api.Module, allocate api.Function, env envelope) uint64 {
	payload, err := msgpack.Marshal(env)
	if err != nil {
		// Marshaling our own envelope failing is an engine bug, not a
		// module-caused condition — there is no meaningful error code to
		// hand back across the boundary for it.
		panic(fmt.Sprintf("abi: marshal host envelope: %v", err))
	}

	results, err := allocate.Call(ctx, uint64(len(payload)))
	if err != nil || results[0] == 0 {
		// The module's own allocate() couldn't satisfy the request. There is
		// nowhere left to write an error envelope, so signal failure via the
		// plain-i32-style upper bits being zero; callers must treat ptr==0
		// as abi.allocation_failed.
		return 0
	}

	ptr := uint32(results[0])
	if !mod.Memory().Write(ptr, payload) {
		return 0
	}

	return (uint64(ptr) << 32) | uint64(len(payload))
}
