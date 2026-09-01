// Command hostcallfixture is a real Go module compiled to wasip1 WASM for
// internal/engine/wasm's own module-side host-call FFI tests (goerp#432)
// — it calls OUT to host.db/host.event through the real sdk/go/db and
// sdk/go/events packages (db.Begin/events.EmitTx/tx.Commit,
// events.Emit(..., events.WithSync()), tx.Lock/tx.TryLock — goerp#508),
// rather than a hand-assembled bytecode stand-in.
//
// Must be built with:
//
//	GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o hostcallfixture.wasm .
package main

import (
	"github.com/djangbahevans/goerp/sdk/go/db"
	"github.com/djangbahevans/goerp/sdk/go/engine"
	"github.com/djangbahevans/goerp/sdk/go/events"
	"github.com/vmihailenco/msgpack/v5"
)

// flowResult is this fixture's own (non-SDK) result envelope — the test
// driving these exports decodes it directly, the same convention
// sdk/go/internal/hostcall.Do uses for a real host call's response.
type flowResult struct {
	OK      bool   `msgpack:"ok"`
	EventID string `msgpack:"event_id,omitempty"`
	Error   string `msgpack:"error,omitempty"`
}

func writeResult(r flowResult) uint64 {
	data, err := msgpack.Marshal(r)
	if err != nil {
		data, _ = msgpack.Marshal(flowResult{Error: "marshal result: " + err.Error()})
	}
	ptr := engine.Allocate(uint32(len(data)))
	engine.WriteMem(ptr, data)
	return uint64(ptr)<<32 | uint64(len(data))
}

//go:wasmexport run_emit_tx_flow
func runEmitTxFlow() uint64 {
	tx, err := db.Begin()
	if err != nil {
		return writeResult(flowResult{Error: "begin: " + err.Error()})
	}

	eventID, err := events.EmitTx(tx, "sales.order.confirmed", map[string]any{"note": "e2e"})
	if err != nil {
		_ = tx.Rollback()
		return writeResult(flowResult{Error: "emit_tx: " + err.Error()})
	}

	if err := tx.Commit(); err != nil {
		return writeResult(flowResult{Error: "commit: " + err.Error()})
	}

	return writeResult(flowResult{OK: true, EventID: eventID})
}

//go:wasmexport run_emit_sync_flow
func runEmitSyncFlow() uint64 {
	eventID, err := events.Emit("sales.order.shipped", map[string]any{"note": "e2e-sync"}, events.WithSync())
	if err != nil {
		return writeResult(flowResult{Error: "emit: " + err.Error()})
	}
	return writeResult(flowResult{OK: true, EventID: eventID})
}

//go:wasmexport run_lock_flow
func runLockFlow() uint64 {
	tx, err := db.Begin()
	if err != nil {
		return writeResult(flowResult{Error: "begin: " + err.Error()})
	}
	defer tx.Rollback()

	acquired, err := tx.TryLock("fixture-lock-key")
	if err != nil {
		return writeResult(flowResult{Error: "try_lock: " + err.Error()})
	}
	if !acquired {
		return writeResult(flowResult{Error: "try_lock: expected to acquire a free lock"})
	}

	if err := tx.Lock("fixture-lock-key-2"); err != nil {
		return writeResult(flowResult{Error: "lock: " + err.Error()})
	}

	if err := tx.Commit(); err != nil {
		return writeResult(flowResult{Error: "commit: " + err.Error()})
	}
	return writeResult(flowResult{OK: true})
}

//go:wasmexport allocate
func allocate(size uint32) uint32 {
	return engine.Allocate(size)
}

//go:wasmexport deallocate
func deallocate(ptr, size uint32) {
	engine.Deallocate(ptr, size)
}

func main() {}
