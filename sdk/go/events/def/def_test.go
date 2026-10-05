package def

import (
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"

	abi "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/vmihailenco/msgpack/v5"
)

type orderPayload struct {
	OrderID string `msgpack:"order_id"`
}

type fakeTx string

func (f fakeTx) TxID() string { return string(f) }

type fakeEmitter struct {
	emitted []abi.EventEmitInput
	txIDs   []string
	emitErr error
	eventID string
}

func (f *fakeEmitter) Emit(in abi.EventEmitInput) (string, error) {
	f.emitted = append(f.emitted, in)
	f.txIDs = append(f.txIDs, "")
	return f.eventID, f.emitErr
}

func (f *fakeEmitter) EmitTx(txID string, in abi.EventEmitInput) (string, error) {
	f.emitted = append(f.emitted, in)
	f.txIDs = append(f.txIDs, txID)
	return f.eventID, f.emitErr
}

func installEmitter(t *testing.T, e Emitter) {
	t.Helper()
	prev := emitter
	SetEmitter(e)
	t.Cleanup(func() { SetEmitter(prev) })
}

func TestDefine_DefaultsAndOptions(t *testing.T) {
	d := Define[orderPayload]("sale.order.confirmed")
	if d.Name() != "sale.order.confirmed" || d.Version() != 1 || d.Description() != "" {
		t.Errorf("defaults: name=%q version=%d description=%q", d.Name(), d.Version(), d.Description())
	}

	d = Define[orderPayload]("sale.order.confirmed", Version(3), Description("confirmed"))
	if d.Version() != 3 || d.Description() != "confirmed" {
		t.Errorf("options: version=%d description=%q", d.Version(), d.Description())
	}
}

func TestDef_EmitCarriesNameVersionAndPayload(t *testing.T) {
	fake := &fakeEmitter{eventID: "evt_1"}
	installEmitter(t, fake)
	d := Define[orderPayload]("sale.order.confirmed", Version(2))

	id, err := d.Emit(orderPayload{OrderID: "ord_1"}, WithDelay(1500*time.Millisecond), WithIdempotencyKey("k1"))
	if err != nil || id != "evt_1" {
		t.Fatalf("Emit = %q, %v", id, err)
	}

	in := fake.emitted[0]
	if in.Name != "sale.order.confirmed" || in.Version != 2 || in.Sync || in.DelayMs != 1500 || in.IdempotencyKey != "k1" {
		t.Errorf("input = %+v", in)
	}
	var got orderPayload
	if err := msgpack.Unmarshal(in.Payload, &got); err != nil || got.OrderID != "ord_1" {
		t.Errorf("payload = %+v, %v", got, err)
	}
}

func TestDef_DefaultVersionIsOne(t *testing.T) {
	fake := &fakeEmitter{}
	installEmitter(t, fake)

	if _, err := Define[orderPayload]("a.b").Emit(orderPayload{}); err != nil {
		t.Fatal(err)
	}
	if fake.emitted[0].Version != 1 {
		t.Errorf("version = %d, want 1", fake.emitted[0].Version)
	}
}

func TestDef_EmitTxPassesTransaction(t *testing.T) {
	fake := &fakeEmitter{}
	installEmitter(t, fake)

	if _, err := Define[orderPayload]("a.b").EmitTx(fakeTx("tx_9"), orderPayload{}); err != nil {
		t.Fatal(err)
	}
	if fake.txIDs[0] != "tx_9" || fake.emitted[0].Sync {
		t.Errorf("txID=%q sync=%v", fake.txIDs[0], fake.emitted[0].Sync)
	}
}

func TestDef_EmitSyncSetsSyncOnNonTransactionalEmit(t *testing.T) {
	fake := &fakeEmitter{emitErr: errors.New("subscriber failed")}
	installEmitter(t, fake)

	_, err := Define[orderPayload]("a.b").EmitSync(orderPayload{})
	if !errors.Is(err, fake.emitErr) {
		t.Errorf("err = %v, want the emitter's error", err)
	}
	if !fake.emitted[0].Sync || fake.txIDs[0] != "" {
		t.Errorf("sync=%v txID=%q", fake.emitted[0].Sync, fake.txIDs[0])
	}
}

func TestDef_EmitWithoutEmitterErrors(t *testing.T) {
	installEmitter(t, nil)
	d := Define[orderPayload]("a.b")

	if _, err := d.Emit(orderPayload{}); !errors.Is(err, ErrNoEmitter) {
		t.Errorf("Emit err = %v", err)
	}
	if _, err := d.EmitSync(orderPayload{}); !errors.Is(err, ErrNoEmitter) {
		t.Errorf("EmitSync err = %v", err)
	}
	if _, err := d.EmitTx(fakeTx("tx"), orderPayload{}); !errors.Is(err, ErrNoEmitter) {
		t.Errorf("EmitTx err = %v", err)
	}
}

func TestPackageLinksNoHostCallLayer(t *testing.T) {
	out, err := exec.CommandContext(t.Context(), "go", "list", "-deps", ".").Output()
	if err != nil {
		t.Fatalf("go list -deps: %v", err)
	}
	for dep := range strings.Lines(string(out)) {
		dep = strings.TrimSpace(dep)
		if strings.HasSuffix(dep, "/sdk/go/db") || strings.Contains(dep, "/sdk/go/internal/") {
			t.Errorf("events/def depends on %s", dep)
		}
	}
}
