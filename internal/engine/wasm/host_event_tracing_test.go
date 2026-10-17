package wasm

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"testing"
	"uuid"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/event"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/vmihailenco/msgpack/v5"
	"go.opentelemetry.io/otel/trace"
)

type tracingSyncDispatcher struct {
	context  trace.SpanContext
	envelope event.Envelope
}

func (d *tracingSyncDispatcher) DispatchSync(ctx context.Context, _, _ string, payload []byte) (int32, error) {
	d.context = trace.SpanContextFromContext(ctx)
	return 0, msgpack.Unmarshal(payload, &d.envelope)
}

func TestHostEventEmissionTracing(t *testing.T) {
	for _, transactional := range []bool{false, true} {
		t.Run(fmt.Sprintf("transactional=%t", transactional), func(t *testing.T) {
			conn := newEventTestDB(t)
			tracer, recorder := testTraceRecorder(t)
			ctx, request := tracer.Start(t.Context(), "request")
			defer request.End()
			ctx, closeInvocation := beginInvocationTrace(ctx, tracer)
			defer closeInvocation()
			state := invocationTraceFromContext(ctx)
			business := state.start(ctx, "business", nil)
			defer func() { _ = state.end(business, "", "") }()

			reg := newEmitterEventRegistry("testmodule", "sales.order.confirmed", "")
			reg.Register("subscriber", manifest.Manifest{Subscribes: []manifest.EventSubscription{
				{Name: "sales.order.confirmed", Handler: "handle_order"},
			}})
			mc := newEventTestModuleContext(reg)
			mc.TenantID = uuid.NewV7().String()
			r := newHostDBTestRuntime(t, conn, 10)
			r.tracer = tracer
			dispatcher := &tracingSyncDispatcher{}
			r.SetSyncEventDispatcher(dispatcher)
			inst := newHostEventCaller(t, ctx, r, mc)
			name := "host.event.emit"
			if transactional {
				name = "host.event.emit_tx"
				txID, tx := beginAndRegisterTx(t, ctx, conn, mc)
				defer func() { _ = tx.Rollback() }()
				result := callHost(t, ctx, inst, "call_emit_tx", abiv1.EventEmitTxInput{
					TxID: txID,
					Name: "sales.order.confirmed",
				})
				if !result.OK {
					t.Fatal(result.Error)
				}
				if err := tx.Commit(); err != nil {
					t.Fatal(err)
				}
				mc.RemoveTransaction(txID)
			} else {
				result := callHost(t, ctx, inst, "call_emit", abiv1.EventEmitInput{
					Name: "sales.order.confirmed",
					Sync: true,
				})
				if !result.OK {
					t.Fatal(result.Error)
				}
			}

			count := 0
			for _, span := range recorder.Ended() {
				if span.Name() == name {
					count++
				}
			}
			if count != 1 {
				t.Fatalf("emission spans = %d, want one host span", count)
			}

			spans := spansByName(t, recorder)
			emission := spans[name]
			if emission == nil || emission.Parent().SpanID() != state.spans[0].span.SpanContext().SpanID() {
				t.Fatalf("emission does not descend from the module span: %v", emission)
			}
			attrs := make(map[string]any)
			for _, attr := range emission.Attributes() {
				attrs[string(attr.Key)] = attr.Value.AsInterface()
			}
			if attrs["event.name"] != "sales.order.confirmed" || attrs["event.version"] != int64(1) || attrs["event.emitter_module"] != "testmodule" {
				t.Fatalf("emission attributes = %v", attrs)
			}
			var raw []byte
			if err := conn.QueryRowContext(ctx, `SELECT args FROM system.river_job WHERE kind='event_delivery' AND args->>'tenant_id'=$1`, mc.TenantID).Scan(&raw); err != nil {
				t.Fatal(err)
			}
			var args event.EventDeliveryArgs
			if err := json.Unmarshal(raw, &args); err != nil {
				t.Fatal(err)
			}
			source := emission.SpanContext()
			if args.TraceID != source.TraceID().String() || args.SpanID != source.SpanID().String() || args.TraceFlags != uint8(source.TraceFlags()) {
				t.Fatalf("queued origin = %+v, want %v", args, source)
			}
			if !transactional && (!dispatcher.context.Equal(source) || dispatcher.envelope.SpanID != args.SpanID || dispatcher.envelope.TraceID != args.TraceID) {
				t.Fatalf("sync dispatch origin = %v / %+v", dispatcher.context, dispatcher.envelope)
			}
		})
	}
}
