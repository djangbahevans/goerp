// Command migrationfixture is a real Go module compiled to wasip1 WASM
// for internal/engine/jobdispatch's own tests — it exercises the SDK's
// actual engine.OnDataMigration/engine.DispatchDataMigration dispatch and
// model.MigrationContext through a real wazero-loaded binary, rather than
// a hand-assembled bytecode stand-in (matching internal/engine/loader/
// testdata/realfixture's own established convention and its doc
// comment's rationale, goerp#234).
//
// Must be built with:
//
//	GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o migrationfixture.wasm .
package main

import (
	"errors"

	"github.com/djangbahevans/goerp/sdk/go/engine"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

func init() {
	engine.OnDataMigration("backfill_test", func(ctx *model.MigrationContext) error {
		ctx.Log("backfill_test invoked", "from", ctx.FromVersion, "to", ctx.ToVersion)
		ctx.RecordProgress(3)
		return nil
	})
	engine.OnDataMigration("failing_test", func(ctx *model.MigrationContext) error {
		return errors.New("intentional failure for testing")
	})
}

//go:wasmexport handle_job
func handleJob(ptr, length uint32) uint32 {
	return engine.DispatchDataMigration(ptr, length)
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
