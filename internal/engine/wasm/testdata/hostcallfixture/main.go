// Command hostcallfixture is a real Go module compiled to wasip1 WASM for
// internal/engine/wasm's own module-side host-call FFI tests (goerp#432)
// — it calls OUT to host.db/host.event/host.jobs through the real
// sdk/go/db, sdk/go/events and sdk/go/jobs packages
// (db.Begin/Def.EmitTx/tx.Commit, Def.EmitSync,
// tx.Lock/tx.TryLock, jobs.EnqueueTx, jobs.EnqueueProviderTx,
// jobs.DispatchProviderSync, notify.SendTx, notify.SendBulk), rather than
// a hand-assembled bytecode stand-in.
//
// Must be built with:
//
//	GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o hostcallfixture.wasm .
package main

import (
	"github.com/djangbahevans/goerp/sdk/go/db"
	"github.com/djangbahevans/goerp/sdk/go/engine"
	"github.com/djangbahevans/goerp/sdk/go/events"
	"github.com/djangbahevans/goerp/sdk/go/jobs"
	"github.com/djangbahevans/goerp/sdk/go/notify"
	"github.com/vmihailenco/msgpack/v5"
)

type notePayload struct {
	Note string `msgpack:"note"`
}

var (
	salesOrderConfirmed = events.Define[notePayload]("sales.order.confirmed")
	salesOrderShipped   = events.Define[notePayload]("sales.order.shipped")
)

// flowResult is this fixture's own (non-SDK) result envelope — the test
// driving these exports decodes it directly, the same convention
// sdk/go/internal/hostcall.Do uses for a real host call's response.
type flowResult struct {
	OK      bool   `msgpack:"ok"`
	EventID string `msgpack:"event_id,omitempty"`
	JobID   string `msgpack:"job_id,omitempty"`
	Result  string `msgpack:"result,omitempty"`
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

	eventID, err := salesOrderConfirmed.EmitTx(tx, notePayload{Note: "e2e"})
	if err != nil {
		_ = tx.Rollback()
		return writeResult(flowResult{Error: "emit_tx: " + err.Error()})
	}

	if err := tx.Commit(); err != nil {
		return writeResult(flowResult{Error: "commit: " + err.Error()})
	}

	return writeResult(flowResult{OK: true, EventID: eventID})
}

var importContacts = jobs.Define[map[string]any]("contacts_import", jobs.Label("Import contacts"))

//go:wasmexport run_enqueue_tx_flow
func runEnqueueTxFlow() uint64 {
	tx, err := db.Begin()
	if err != nil {
		return writeResult(flowResult{Error: "begin: " + err.Error()})
	}

	jobID, err := importContacts.EnqueueTx(tx, map[string]any{"file_id": "e2e"},
		jobs.OnQueue(jobs.QueueBulk), jobs.WithIdempotencyKey("import:e2e"))
	if err != nil {
		_ = tx.Rollback()
		return writeResult(flowResult{Error: "enqueue_tx: " + err.Error()})
	}

	if err := tx.Commit(); err != nil {
		return writeResult(flowResult{Error: "commit: " + err.Error()})
	}

	return writeResult(flowResult{OK: true, JobID: jobID})
}

//go:wasmexport run_enqueue_provider_tx_flow
func runEnqueueProviderTxFlow() uint64 {
	tx, err := db.Begin()
	if err != nil {
		return writeResult(flowResult{Error: "begin: " + err.Error()})
	}

	jobID, err := jobs.EnqueueProviderTx(tx, "sms_provider", "sms_send",
		map[string]any{"schema_version": 1, "to": "+233200000000", "body": "e2e"})
	if err != nil {
		_ = tx.Rollback()
		return writeResult(flowResult{Error: "enqueue_provider_tx: " + err.Error()})
	}

	if err := tx.Commit(); err != nil {
		return writeResult(flowResult{Error: "commit: " + err.Error()})
	}

	return writeResult(flowResult{OK: true, JobID: jobID})
}

//go:wasmexport run_dispatch_provider_sync_flow
func runDispatchProviderSyncFlow() uint64 {
	var result struct {
		CheckoutURL string `msgpack:"checkout_url"`
	}
	err := jobs.DispatchProviderSync("payment_provider", "connector_paystack", "payment_charge",
		map[string]any{"schema_version": 1, "amount": 1000}, &result)
	if err != nil {
		return writeResult(flowResult{Error: "dispatch_provider_sync: " + err.Error()})
	}
	return writeResult(flowResult{OK: true, Result: result.CheckoutURL})
}

//go:wasmexport run_emit_sync_flow
func runEmitSyncFlow() uint64 {
	eventID, err := salesOrderShipped.EmitSync(notePayload{Note: "e2e-sync"})
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

// orderConfirmed is template data sent as a struct, to show it reaches
// the engine as a map keyed by field name.
type orderConfirmed struct {
	OrderReference string
	AmountTotal    int
}

//go:wasmexport run_notify_send_tx_flow
func runNotifySendTxFlow() uint64 {
	tx, err := db.Begin()
	if err != nil {
		return writeResult(flowResult{Error: "begin: " + err.Error()})
	}

	err = notify.SendTx(tx, "user-2", "sales.order_confirmed", "sales.order_confirmed",
		orderConfirmed{OrderReference: "ORD-1", AmountTotal: 42},
		notify.HighPriority(), notify.ForceChannel(notify.ChannelSMS), notify.AdditionalChannel(notify.ChannelPush),
		notify.WithIdempotencyKey("order-confirmed:ORD-1"), notify.WithActionURL("/_m/sales/orders/ORD-1"))
	if err != nil {
		_ = tx.Rollback()
		return writeResult(flowResult{Error: "send_tx: " + err.Error()})
	}

	if err := tx.Commit(); err != nil {
		return writeResult(flowResult{Error: "commit: " + err.Error()})
	}

	return writeResult(flowResult{OK: true})
}

//go:wasmexport run_notify_send_bulk_flow
func runNotifySendBulkFlow() uint64 {
	err := notify.SendBulk([]string{"user-2", "user-3"}, "sales.order_shipped", "sales.order_shipped", map[string]any{"TrackingNumber": "TRK-1"})
	if err != nil {
		return writeResult(flowResult{Error: err.Error()})
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
