package wasm

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
	"uuid"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/vmihailenco/msgpack/v5"
)

var hostNotifyCallerModule = buildHostCallerModule("host.notify", []string{"send", "send_tx", "send_bulk"})

// fakeNotifySender records each request, and answers with err when set,
// else one result per user.
type fakeNotifySender struct {
	mu        sync.Mutex
	requests  []NotifyRequest
	announced []string
	err       error
}

func (f *fakeNotifySender) SendBulk(_ context.Context, req NotifyRequest) ([]abiv1.NotifyRecipientResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, req)
	if f.err != nil {
		return nil, f.err
	}
	out := make([]abiv1.NotifyRecipientResult, len(req.UserIDs))
	for i, id := range req.UserIDs {
		out[i] = abiv1.NotifyRecipientResult{UserID: id, NotificationID: "n-" + id, ChannelsUsed: []string{"in_app"}}
	}
	return out, nil
}

func (f *fakeNotifySender) SendTx(ctx context.Context, _ *sql.Tx, req NotifyRequest) (abiv1.NotifyRecipientResult, func(context.Context), error) {
	results, err := f.SendBulk(ctx, req)
	if err != nil {
		return abiv1.NotifyRecipientResult{}, nil, err
	}
	res := results[0]
	return res, func(context.Context) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.announced = append(f.announced, res.NotificationID)
	}, nil
}

func (f *fakeNotifySender) announcedIDs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.announced)
}

func newNotifyTestModuleContext(caps abi.CapabilitySet) *ModuleContext {
	return NewModuleContext("req-1", "sales", "user-1", "", nil, nil, uuid.New().String(), "notifytest", "trace-1", caps, nil, ModuleSnapshot{})
}

func newHostNotifyCaller(t *testing.T, ctx context.Context, r *Runtime, mc *ModuleContext) *ModuleInstance {
	t.Helper()

	compiled, err := r.wazero.CompileModule(ctx, hostNotifyCallerModule)
	if err != nil {
		t.Fatalf("CompileModule: %v", err)
	}
	t.Cleanup(func() { _ = compiled.Close(ctx) })

	inst, err := newModuleInstance(ctx, fmt.Sprintf("notify-caller-%d", time.Now().UnixNano()), compiled, r.wazero)
	if err != nil {
		t.Fatalf("newModuleInstance: %v", err)
	}
	inst.SetModuleContext(mc)
	r.RegisterInstance(inst)
	t.Cleanup(func() { r.UnregisterInstance(inst) })

	return inst
}

func decodeNotifyData[T any](t *testing.T, env abiv1.Envelope) T {
	t.Helper()
	if !env.OK {
		t.Fatalf("host.notify call failed: %+v", env.Error)
	}
	var out T
	if err := msgpack.Unmarshal(env.Data, &out); err != nil {
		t.Fatalf("decode output: %v", err)
	}
	return out
}

func TestHostNotify_CapabilityDenied(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := t.Context()

	r := newHostDBTestRuntime(t, primaryDB, 10)
	fake := &fakeNotifySender{}
	r.SetNotifySender(fake)
	inst := newHostNotifyCaller(t, ctx, r, newNotifyTestModuleContext(abi.CapabilitySet(0)))

	for _, fn := range []string{"call_send", "call_send_tx", "call_send_bulk"} {
		env := callHost(t, ctx, inst, fn, abiv1.NotifySendInput{UserID: "u-1", Type: "sales.order_confirmed"})
		if env.OK || env.Error.Code != abiv1.ErrCodeCapabilityDenied {
			t.Errorf("%s: env = %+v, want %s", fn, env, abiv1.ErrCodeCapabilityDenied)
		}
	}
	if len(fake.requests) != 0 {
		t.Errorf("the pipeline was called %d times without notify.send", len(fake.requests))
	}
}

func TestHostNotify_UnavailableUntilWired(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := t.Context()

	r := newHostDBTestRuntime(t, primaryDB, 10)
	inst := newHostNotifyCaller(t, ctx, r, newNotifyTestModuleContext(abi.CapNotifySend))

	env := callHost(t, ctx, inst, "call_send", abiv1.NotifySendInput{UserID: "u-1", Type: "sales.order_confirmed"})
	if env.OK || env.Error.Code != abiv1.ErrCodeUnavailable || !env.Error.Retry {
		t.Fatalf("env = %+v, want a retryable %s", env, abiv1.ErrCodeUnavailable)
	}
}

func TestHostNotify_Send_PassesTheCallersRequest(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := t.Context()

	r := newHostDBTestRuntime(t, primaryDB, 10)
	fake := &fakeNotifySender{}
	r.SetNotifySender(fake)
	mc := newNotifyTestModuleContext(abi.CapNotifySend)
	inst := newHostNotifyCaller(t, ctx, r, mc)

	opts := abiv1.NotifySendOptions{Priority: "high", ChannelOverride: "sms", IdempotencyKey: "k"}
	out := decodeNotifyData[abiv1.NotifySendOutput](t, callHost(t, ctx, inst, "call_send", abiv1.NotifySendInput{
		UserID: "u-1", Type: "sales.order_confirmed", TemplateKey: "sales.order_confirmed",
		Data: mustMarshalPayload(t, map[string]any{"OrderReference": "ORD-1"}), Opts: opts,
	}))
	if out.NotificationID != "n-u-1" || !slices.Equal(out.ChannelsUsed, []string{"in_app"}) {
		t.Errorf("output = %+v", out)
	}

	req := fake.requests[0]
	if req.TenantID != mc.TenantID || req.ModuleName != "sales" || req.NotificationType != "sales.order_confirmed" || req.TraceID != "trace-1" {
		t.Errorf("request = %+v, want the calling module's tenant, name and trace", req)
	}
	if !slices.Equal(req.UserIDs, []string{"u-1"}) || req.Data["OrderReference"] != "ORD-1" || req.Opts.Priority != opts.Priority ||
		req.Opts.ChannelOverride != opts.ChannelOverride || req.Opts.IdempotencyKey != opts.IdempotencyKey {
		t.Errorf("request = %+v", req)
	}
}

func TestHostNotify_Send_RejectsNonMapData(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := t.Context()

	r := newHostDBTestRuntime(t, primaryDB, 10)
	fake := &fakeNotifySender{}
	r.SetNotifySender(fake)
	inst := newHostNotifyCaller(t, ctx, r, newNotifyTestModuleContext(abi.CapNotifySend))

	data, err := msgpack.Marshal([]string{"not", "a", "map"})
	if err != nil {
		t.Fatal(err)
	}
	env := callHost(t, ctx, inst, "call_send", abiv1.NotifySendInput{UserID: "u-1", Type: "sales.order_confirmed", Data: data})
	if env.OK || env.Error.Code != abiv1.ErrCodeDeserializeError {
		t.Fatalf("env = %+v, want %s", env, abiv1.ErrCodeDeserializeError)
	}
	if len(fake.requests) != 0 {
		t.Error("the pipeline was called with undecodable data")
	}
}

func TestHostNotify_ReportsPipelineErrors(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := t.Context()

	r := newHostDBTestRuntime(t, primaryDB, 10)
	fake := &fakeNotifySender{}
	r.SetNotifySender(fake)
	inst := newHostNotifyCaller(t, ctx, r, newNotifyTestModuleContext(abi.CapNotifySend))

	fake.err = fmt.Errorf("wrapped: %w", &abiv1.HostError{Code: abiv1.ErrCodeNotifyUndeclaredType, Message: "not yours"})
	env := callHost(t, ctx, inst, "call_send_bulk", abiv1.NotifySendBulkInput{UserIDs: []string{"u-1"}, Type: "billing.invoice_overdue"})
	if env.OK || env.Error.Code != abiv1.ErrCodeNotifyUndeclaredType || env.Error.Retry {
		t.Errorf("caller error: env = %+v, want a non-retryable %s", env, abiv1.ErrCodeNotifyUndeclaredType)
	}

	fake.err = errors.New("connection refused")
	env = callHost(t, ctx, inst, "call_send", abiv1.NotifySendInput{UserID: "u-1", Type: "sales.order_confirmed"})
	if env.OK || env.Error.Code != abiv1.ErrCodeUnavailable || !env.Error.Retry {
		t.Errorf("pipeline error: env = %+v, want a retryable %s", env, abiv1.ErrCodeUnavailable)
	}
}

func TestHostNotify_SendBulk_ReturnsEachRecipient(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := t.Context()

	r := newHostDBTestRuntime(t, primaryDB, 10)
	fake := &fakeNotifySender{}
	r.SetNotifySender(fake)
	inst := newHostNotifyCaller(t, ctx, r, newNotifyTestModuleContext(abi.CapNotifySend))

	out := decodeNotifyData[abiv1.NotifySendBulkOutput](t, callHost(t, ctx, inst, "call_send_bulk", abiv1.NotifySendBulkInput{
		UserIDs: []string{"u-1", "u-2"}, Type: "sales.order_confirmed",
	}))
	if len(out.Notifications) != 2 || out.Notifications[0].UserID != "u-1" || out.Notifications[1].NotificationID != "n-u-2" {
		t.Fatalf("output = %+v", out)
	}
}

func TestHostNotify_SendTx_UnknownTransaction(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := t.Context()

	r := newHostDBTestRuntime(t, primaryDB, 10)
	r.SetNotifySender(&fakeNotifySender{})
	inst := newHostNotifyCaller(t, ctx, r, newNotifyTestModuleContext(abi.CapNotifySend))

	env := callHost(t, ctx, inst, "call_send_tx", abiv1.NotifySendTxInput{TxID: "does-not-exist", UserID: "u-1", Type: "sales.order_confirmed"})
	if env.OK || env.Error.Code != abiv1.ErrCodeTransactionNotFound {
		t.Fatalf("env = %+v, want %s", env, abiv1.ErrCodeTransactionNotFound)
	}
}

// send_tx pushes its notification only once host.db.commit commits the
// module's transaction, and never after a rollback.
func TestHostNotify_SendTx_AnnouncesAfterCommitOnly(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := t.Context()

	r := newHostDBTestRuntime(t, primaryDB, 10)
	fake := &fakeNotifySender{}
	r.SetNotifySender(fake)
	mc := newNotifyTestModuleContext(abi.CapNotifySend | abi.CapDBWrite)
	notifyInst := newHostNotifyCaller(t, ctx, r, mc)
	dbInst := newHostDBCaller(t, ctx, r, mc)

	sendIn := func(userID string) (txID string) {
		begin := decodeNotifyData[abiv1.DBBeginOutput](t, callHost(t, ctx, dbInst, "call_begin", abiv1.DBBeginInput{}))
		decodeNotifyData[abiv1.NotifySendOutput](t, callHost(t, ctx, notifyInst, "call_send_tx", abiv1.NotifySendTxInput{
			TxID: begin.TxID, UserID: userID, Type: "sales.order_confirmed",
		}))
		if got := fake.announcedIDs(); len(got) != 0 {
			t.Fatalf("announced %v before the transaction ended", got)
		}
		return begin.TxID
	}

	rolledBack := sendIn("u-1")
	decodeNotifyData[abiv1.DBDurationOutput](t, callHost(t, ctx, dbInst, "call_rollback", abiv1.DBTxIDInput{TxID: rolledBack}))
	if got := fake.announcedIDs(); len(got) != 0 {
		t.Fatalf("announced %v after a rollback", got)
	}

	committed := sendIn("u-2")
	decodeNotifyData[abiv1.DBDurationOutput](t, callHost(t, ctx, dbInst, "call_commit", abiv1.DBTxIDInput{TxID: committed}))
	if got := fake.announcedIDs(); !slices.Equal(got, []string{"n-u-2"}) {
		t.Fatalf("announced %v after commit, want [n-u-2]", got)
	}
}

// A real module compiled against sdk/go/notify reaches host.notify: its
// struct data arrives as a map, its options in their ABI shape, and
// send_tx's push waits for tx.Commit.
func TestHostcallFixture_NotifySendTx(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()
	wasmBytes := compileHostcallFixture(t)

	r := newHostcallTestRuntime(t, primaryDB, 10)
	fake := &fakeNotifySender{}
	r.SetNotifySender(fake)
	mc := newNotifyTestModuleContext(abi.CapDBWrite | abi.CapNotifySend)

	out := callHostcallFixture(t, ctx, r, wasmBytes, mc, "run_notify_send_tx_flow")
	if !out.OK {
		t.Fatalf("run_notify_send_tx_flow failed: %s", out.Error)
	}

	req := fake.requests[0]
	if !slices.Equal(req.UserIDs, []string{"user-2"}) || req.NotificationType != "sales.order_confirmed" || req.ModuleName != "sales" {
		t.Errorf("request = %+v", req)
	}
	if req.Data["OrderReference"] != "ORD-1" {
		t.Errorf("Data = %#v, want the struct's fields by name", req.Data)
	}
	want := abiv1.NotifySendOptions{Priority: "high", ChannelOverride: "sms", AdditionalChannels: []string{"push"}, ActionURL: "/_m/sales/orders/ORD-1", IdempotencyKey: "order-confirmed:ORD-1"}
	if got := req.Opts; got.Priority != want.Priority || got.ChannelOverride != want.ChannelOverride || !slices.Equal(got.AdditionalChannels, want.AdditionalChannels) ||
		got.ActionURL != want.ActionURL || got.IdempotencyKey != want.IdempotencyKey {
		t.Errorf("Opts = %+v, want %+v", got, want)
	}
	if got := fake.announcedIDs(); !slices.Equal(got, []string{"n-user-2"}) {
		t.Errorf("announced %v, want [n-user-2] once the fixture committed", got)
	}
}

func TestHostcallFixture_NotifySendBulk_SurfacesErrors(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()
	wasmBytes := compileHostcallFixture(t)

	r := newHostcallTestRuntime(t, primaryDB, 10)
	fake := &fakeNotifySender{}
	r.SetNotifySender(fake)
	mc := newNotifyTestModuleContext(abi.CapNotifySend)

	if out := callHostcallFixture(t, ctx, r, wasmBytes, mc, "run_notify_send_bulk_flow"); !out.OK {
		t.Fatalf("run_notify_send_bulk_flow failed: %s", out.Error)
	}
	if req := fake.requests[0]; !slices.Equal(req.UserIDs, []string{"user-2", "user-3"}) || req.Data["TrackingNumber"] != "TRK-1" {
		t.Errorf("request = %+v", req)
	}

	fake.err = &abiv1.HostError{Code: abiv1.ErrCodeNotifyTooManyRecipients, Message: "too many"}
	out := callHostcallFixture(t, ctx, r, wasmBytes, mc, "run_notify_send_bulk_flow")
	if out.OK || !strings.Contains(out.Error, abiv1.ErrCodeNotifyTooManyRecipients) {
		t.Fatalf("out = %+v, want the %s error surfaced", out, abiv1.ErrCodeNotifyTooManyRecipients)
	}

	mc = newNotifyTestModuleContext(abi.CapabilitySet(0))
	out = callHostcallFixture(t, ctx, r, wasmBytes, mc, "run_notify_send_bulk_flow")
	if out.OK || !strings.Contains(out.Error, abiv1.ErrCodeCapabilityDenied) {
		t.Fatalf("out = %+v, want %s without notify.send", out, abiv1.ErrCodeCapabilityDenied)
	}
}

// A failed send_tx can leave the module's transaction aborted, so it is
// never reported as retryable on that transaction.
func TestHostNotify_SendTx_PipelineErrorIsNotRetryable(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := t.Context()

	r := newHostDBTestRuntime(t, primaryDB, 10)
	fake := &fakeNotifySender{err: errors.New("current transaction is aborted")}
	r.SetNotifySender(fake)
	mc := newNotifyTestModuleContext(abi.CapNotifySend | abi.CapDBWrite)
	notifyInst := newHostNotifyCaller(t, ctx, r, mc)
	dbInst := newHostDBCaller(t, ctx, r, mc)

	begin := decodeNotifyData[abiv1.DBBeginOutput](t, callHost(t, ctx, dbInst, "call_begin", abiv1.DBBeginInput{}))
	t.Cleanup(func() { mc.RollbackAll() })
	env := callHost(t, ctx, notifyInst, "call_send_tx", abiv1.NotifySendTxInput{TxID: begin.TxID, UserID: "u-1", Type: "sales.order_confirmed"})
	if env.OK || env.Error.Code != abiv1.ErrCodeUnavailable || env.Error.Retry {
		t.Fatalf("env = %+v, want a non-retryable %s", env, abiv1.ErrCodeUnavailable)
	}
}
