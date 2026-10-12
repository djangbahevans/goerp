// Command logcallerfixture exercises structured SDK logging from WASM.
package main

import (
	"errors"
	"math"

	"github.com/djangbahevans/goerp/sdk/go/engine"
	"github.com/djangbahevans/goerp/sdk/go/log"
)

//go:wasmexport run_logs
func runLogs() {
	log.Debug("debug line")
	log.Info("order confirmed", "count", int64(9007199254740993), "success", true, "rate", 0.95,
		"password", "fixture-secret", "module", "forged", "duplicate", "first", "duplicate", "last", 123, "ignored", "unpaired")
	log.Info("unsupported field", "value", make(chan int))
	log.Info("invalid JSON field", "value", math.NaN())
	log.Warn("warning line")
	log.Error("failed", "error", errors.New("request failed: Bearer fixture-token"))
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
