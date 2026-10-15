package main

import (
	"fmt"

	"github.com/djangbahevans/goerp/sdk/go/engine"
	"github.com/djangbahevans/goerp/sdk/go/jobs"
	"github.com/djangbahevans/goerp/sdk/go/ui"
)

func init() {
	engine.HandleJob(jobs.Define[struct{}]("confirm", jobs.Label("Confirm")), func(ctx *engine.JobContext, _ struct{}) error {
		count, err := ui.Push(ui.UserChannel(ctx.UserID),
			ui.Toast("Payment received", ui.Success, ui.WithDuration(0)),
			ui.Reload(ui.WithTarget("sales.invoice", "inv1")),
			ui.SetBadge("/sales/count", 0),
		)
		if err != nil {
			return err
		}
		if count != 1 {
			return fmt.Errorf("delivered to %d connections, want 1", count)
		}
		return nil
	})
}

//go:wasmexport handle_job
func handleJob(ptr, length uint32) uint32 {
	return engine.DispatchJob(ptr, length)
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
