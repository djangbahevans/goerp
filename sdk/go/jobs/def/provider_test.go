package def

import (
	"errors"
	"reflect"
	"testing"
	"time"

	abi "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/vmihailenco/msgpack/v5"
)

type chargePayload struct {
	Amount int `msgpack:"amount"`
}

type chargeResult struct {
	CheckoutURL string `msgpack:"checkout_url"`
}

var paymentCharge = DefineProvider[chargePayload, chargeResult]("payment_provider", "payment_charge")

func TestDefineProvider_RejectsMalformedNames(t *testing.T) {
	mustPanic(t, "category", func() { DefineProvider[chargePayload, chargeResult]("Payment-Provider", "payment_charge") })
	mustPanic(t, "job type", func() { DefineProvider[chargePayload, chargeResult]("payment_provider", "PaymentCharge") })
}

func TestProviderDef_Accessors(t *testing.T) {
	if paymentCharge.Name() != "payment_charge" || paymentCharge.Category() != "payment_provider" {
		t.Errorf("Name, Category = %q, %q", paymentCharge.Name(), paymentCharge.Category())
	}
	if paymentCharge.PayloadType() != reflect.TypeFor[chargePayload]() {
		t.Errorf("PayloadType = %v", paymentCharge.PayloadType())
	}
}

func TestProviderDef_EnqueueCarriesCategoryJobTypeAndProviderModule(t *testing.T) {
	fake := &fakeEnqueuer{}
	installEnqueuer(t, fake)
	withModule := JobOption(func(o *EnqueueOptions) { o.ProviderModule = "connector_paystack" })

	id, err := paymentCharge.Enqueue(chargePayload{Amount: 5}, withModule, WithPriority(90))
	if err != nil || id != "job-3" {
		t.Fatalf("Enqueue = %q, %v", id, err)
	}
	got := fake.provider[0]
	var payload chargePayload
	if err := msgpack.Unmarshal(got.Payload, &payload); err != nil || payload.Amount != 5 {
		t.Fatalf("payload = %+v, %v", payload, err)
	}
	if got.Category != "payment_provider" || got.JobType != "payment_charge" || got.ProviderModule != "connector_paystack" || got.Opts.Priority != 90 {
		t.Errorf("input = %+v", got)
	}

	if _, err := paymentCharge.EnqueueTx(fakeTx("tx-1"), chargePayload{}); err != nil {
		t.Fatal(err)
	}
	if got := fake.providerTx[0]; got.TxID != "tx-1" || got.Category != "payment_provider" || got.JobType != "payment_charge" {
		t.Errorf("tx input = %+v", got)
	}
}

func TestProviderDef_DispatchSyncDecodesTheHandlersResult(t *testing.T) {
	result, err := msgpack.Marshal(chargeResult{CheckoutURL: "https://checkout.example/abc"})
	if err != nil {
		t.Fatal(err)
	}
	fake := &fakeEnqueuer{syncOut: abi.JobsDispatchProviderSyncOutput{ResultPayload: result}}
	installEnqueuer(t, fake)

	got, err := paymentCharge.DispatchSync("connector_paystack", chargePayload{Amount: 5}, WithSyncTimeout(2500*time.Millisecond))
	if err != nil || got.CheckoutURL != "https://checkout.example/abc" {
		t.Fatalf("DispatchSync = %+v, %v", got, err)
	}
	in := fake.sync[0]
	if in.Category != "payment_provider" || in.ProviderModule != "connector_paystack" || in.JobType != "payment_charge" || in.TimeoutMs != 2500 {
		t.Errorf("input = %+v", in)
	}
}

func TestProviderDef_DispatchSyncWithoutAResultIsTheZeroValue(t *testing.T) {
	installEnqueuer(t, &fakeEnqueuer{})

	got, err := paymentCharge.DispatchSync("connector_paystack", chargePayload{})
	if err != nil || got != (chargeResult{}) {
		t.Fatalf("DispatchSync = %+v, %v; want zero value", got, err)
	}
}

func TestProviderDef_DispatchSyncUndecodableResultErrors(t *testing.T) {
	bad, err := msgpack.Marshal([]int{1, 2})
	if err != nil {
		t.Fatal(err)
	}
	installEnqueuer(t, &fakeEnqueuer{syncOut: abi.JobsDispatchProviderSyncOutput{ResultPayload: bad}})

	if _, err := paymentCharge.DispatchSync("connector_paystack", chargePayload{}); err == nil {
		t.Fatal("DispatchSync succeeded for a result that does not decode into R")
	}
}

func TestProviderDef_PropagatesEnqueuerError(t *testing.T) {
	boom := errors.New("jobs.provider_module_not_enabled")
	installEnqueuer(t, &fakeEnqueuer{err: boom})

	if _, err := paymentCharge.Enqueue(chargePayload{}); !errors.Is(err, boom) {
		t.Errorf("Enqueue error = %v", err)
	}
	if _, err := paymentCharge.DispatchSync("m", chargePayload{}); !errors.Is(err, boom) {
		t.Errorf("DispatchSync error = %v", err)
	}
}

func TestProviderDef_WithoutEnqueuerErrors(t *testing.T) {
	installEnqueuer(t, nil)

	if _, err := paymentCharge.Enqueue(chargePayload{}); !errors.Is(err, ErrNoEnqueuer) {
		t.Errorf("Enqueue error = %v", err)
	}
	if _, err := paymentCharge.EnqueueTx(fakeTx("tx"), chargePayload{}); !errors.Is(err, ErrNoEnqueuer) {
		t.Errorf("EnqueueTx error = %v", err)
	}
	if _, err := paymentCharge.DispatchSync("m", chargePayload{}); !errors.Is(err, ErrNoEnqueuer) {
		t.Errorf("DispatchSync error = %v", err)
	}
}
