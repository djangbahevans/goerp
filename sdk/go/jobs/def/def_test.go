package def

import (
	"errors"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"

	abi "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/vmihailenco/msgpack/v5"
)

type importPayload struct {
	FileID string `msgpack:"file_id"`
}

type fakeTx string

func (f fakeTx) TxID() string { return string(f) }

type fakeEnqueuer struct {
	enqueued   []abi.JobsEnqueueInput
	enqueuedTx []abi.JobsEnqueueTxInput
	provider   []abi.JobsEnqueueProviderInput
	providerTx []abi.JobsEnqueueProviderTxInput
	sync       []abi.JobsDispatchProviderSyncInput
	syncOut    abi.JobsDispatchProviderSyncOutput
	err        error
}

func (f *fakeEnqueuer) EnqueueProvider(in abi.JobsEnqueueProviderInput) (string, error) {
	f.provider = append(f.provider, in)
	return "job-3", f.err
}

func (f *fakeEnqueuer) EnqueueProviderTx(in abi.JobsEnqueueProviderTxInput) (string, error) {
	f.providerTx = append(f.providerTx, in)
	return "job-4", f.err
}

func (f *fakeEnqueuer) DispatchProviderSync(in abi.JobsDispatchProviderSyncInput) (abi.JobsDispatchProviderSyncOutput, error) {
	f.sync = append(f.sync, in)
	return f.syncOut, f.err
}

func (f *fakeEnqueuer) Enqueue(in abi.JobsEnqueueInput) (string, error) {
	f.enqueued = append(f.enqueued, in)
	return "job-1", f.err
}

func (f *fakeEnqueuer) EnqueueTx(in abi.JobsEnqueueTxInput) (string, error) {
	f.enqueuedTx = append(f.enqueuedTx, in)
	return "job-2", f.err
}

func installEnqueuer(t *testing.T, e Enqueuer) {
	t.Helper()
	prev := enqueuer
	SetEnqueuer(e)
	t.Cleanup(func() { enqueuer = prev })
}

func mustPanic(t *testing.T, name string, fn func()) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Errorf("%s did not panic", name)
		}
	}()
	fn()
}

func TestDef_EnqueueCarriesNameAndDefinitionDefaults(t *testing.T) {
	fake := &fakeEnqueuer{}
	installEnqueuer(t, fake)
	job := Define[importPayload]("contacts_import_process",
		Label("Process Import"), Queue(QueueBulk), MaxAttempts(5), Priority(70), Timeout(time.Hour))

	id, err := job.Enqueue(importPayload{FileID: "f1"})
	if err != nil || id != "job-1" {
		t.Fatalf("Enqueue = (%q, %v)", id, err)
	}

	got := fake.enqueued[0]
	if got.Type != "contacts_import_process" {
		t.Errorf("Type = %q", got.Type)
	}
	want := abi.JobEnqueueOptions{Queue: QueueBulk, MaxAttempts: 5, Priority: 70}
	if got.Opts != want {
		t.Errorf("Opts = %+v, want %+v", got.Opts, want)
	}
	var payload importPayload
	if err := msgpack.Unmarshal(got.Payload, &payload); err != nil || payload.FileID != "f1" {
		t.Errorf("payload = %+v, %v", payload, err)
	}
}

func TestDef_PerEnqueueOptionsOverrideDefinition(t *testing.T) {
	fake := &fakeEnqueuer{}
	installEnqueuer(t, fake)
	job := Define[importPayload]("contacts_import_process",
		Label("Process Import"), Queue(QueueBulk), MaxAttempts(5), Priority(70))
	at := time.Unix(1_900_000_000, 0)

	if _, err := job.Enqueue(importPayload{},
		OnQueue(QueueCritical), WithPriority(90), WithMaxAttempts(2), WithDelay(1500*time.Millisecond),
		ScheduleAt(at), WithIdempotencyKey("import:f1")); err != nil {
		t.Fatal(err)
	}

	want := abi.JobEnqueueOptions{
		Queue: QueueCritical, Priority: 90, MaxAttempts: 2, DelayMs: 1500, ScheduledAt: at.Unix(), IdempotencyKey: "import:f1",
	}
	if got := fake.enqueued[0].Opts; got != want {
		t.Errorf("Opts = %+v, want %+v", got, want)
	}
}

func TestDef_UnsetOptionsStayZeroForHostDefaults(t *testing.T) {
	fake := &fakeEnqueuer{}
	installEnqueuer(t, fake)

	if _, err := Define[importPayload]("contacts_import", Label("Import")).Enqueue(importPayload{}); err != nil {
		t.Fatal(err)
	}
	if got := fake.enqueued[0].Opts; got != (abi.JobEnqueueOptions{}) {
		t.Errorf("Opts = %+v, want the zero value", got)
	}
}

func TestDef_EnqueueTxScopesToTransaction(t *testing.T) {
	fake := &fakeEnqueuer{}
	installEnqueuer(t, fake)
	job := Define[importPayload]("contacts_import", Label("Import"), Queue(QueueEmail))

	id, err := job.EnqueueTx(fakeTx("tx-9"), importPayload{FileID: "f1"})
	if err != nil || id != "job-2" {
		t.Fatalf("EnqueueTx = (%q, %v)", id, err)
	}
	got := fake.enqueuedTx[0]
	if got.TxID != "tx-9" || got.Type != "contacts_import" || got.Opts.Queue != QueueEmail {
		t.Errorf("EnqueueTx input = %+v", got)
	}
}

func TestDef_EnqueuePropagatesEnqueuerError(t *testing.T) {
	fake := &fakeEnqueuer{err: errors.New("jobs.unknown_type")}
	installEnqueuer(t, fake)

	if _, err := Define[importPayload]("contacts_import", Label("Import")).Enqueue(importPayload{}); !errors.Is(err, fake.err) {
		t.Errorf("err = %v, want the enqueuer's error", err)
	}
}

func TestDef_EnqueueWithoutEnqueuerErrors(t *testing.T) {
	installEnqueuer(t, nil)
	job := Define[importPayload]("contacts_import", Label("Import"))

	if _, err := job.Enqueue(importPayload{}); !errors.Is(err, ErrNoEnqueuer) {
		t.Errorf("Enqueue err = %v", err)
	}
	if _, err := job.EnqueueTx(fakeTx("tx"), importPayload{}); !errors.Is(err, ErrNoEnqueuer) {
		t.Errorf("EnqueueTx err = %v", err)
	}
}

func TestDef_EnqueueRejectsProviderModuleOption(t *testing.T) {
	installEnqueuer(t, &fakeEnqueuer{})
	job := Define[importPayload]("contacts_import", Label("Import"))
	withProvider := JobOption(func(o *EnqueueOptions) { o.ProviderModule = "connector_paystack" })

	if _, err := job.Enqueue(importPayload{}, withProvider); !errors.Is(err, ErrProviderModuleOption) {
		t.Errorf("Enqueue err = %v", err)
	}
	if _, err := job.EnqueueTx(fakeTx("tx"), importPayload{}, withProvider); !errors.Is(err, ErrProviderModuleOption) {
		t.Errorf("EnqueueTx err = %v", err)
	}
}

func TestDefinition_ExposesNameSpecAndPayloadType(t *testing.T) {
	var d Definition = Define[importPayload]("contacts_import_process",
		Label("Process Import"), Description("Imports a CSV"), Queue(QueueBulk), Timeout(time.Hour),
		MaxAttempts(5), Priority(70), UniqueBy("file_id"))

	if d.Name() != "contacts_import_process" || d.PayloadType() != reflect.TypeFor[importPayload]() {
		t.Errorf("Name/PayloadType = %q/%v", d.Name(), d.PayloadType())
	}
	want := Spec{
		Label: "Process Import", Description: "Imports a CSV", Queue: QueueBulk, Timeout: time.Hour,
		MaxAttempts: 5, Priority: 70, UniqueBy: "file_id",
	}
	if got := d.Spec(); got != want {
		t.Errorf("Spec = %+v, want %+v", got, want)
	}
}

func TestDefine_PanicsOnInvalidDefinition(t *testing.T) {
	label := Label("L")
	mustPanic(t, "empty name", func() { Define[struct{}]("", label) })
	mustPanic(t, "not snake_case", func() { Define[struct{}]("Contacts-Import", label) })
	mustPanic(t, "missing label", func() { Define[struct{}]("job") })
	mustPanic(t, "unknown queue", func() { Define[struct{}]("job", label, Queue("urgent")) })
	mustPanic(t, "timeout over 24h", func() { Define[struct{}]("job", label, Timeout(25*time.Hour)) })
	mustPanic(t, "negative timeout", func() { Define[struct{}]("job", label, Timeout(-time.Second)) })
	mustPanic(t, "max attempts over 25", func() { Define[struct{}]("job", label, MaxAttempts(26)) })
	mustPanic(t, "priority over 100", func() { Define[struct{}]("job", label, Priority(101)) })
	mustPanic(t, "negative priority", func() { Define[struct{}]("job", label, Priority(-1)) })
}

func TestDefine_AcceptsLimits(t *testing.T) {
	Define[struct{}]("job", Label("L"), Timeout(24*time.Hour), MaxAttempts(25), Priority(100), Queue(QueueSearch))
	Define[struct{}]("job", Label("L"), Priority(1))
}

func TestPackageLinksNoHostCallLayer(t *testing.T) {
	out, err := exec.CommandContext(t.Context(), "go", "list", "-deps", ".").Output()
	if err != nil {
		t.Fatalf("go list -deps: %v", err)
	}
	for dep := range strings.Lines(string(out)) {
		dep = strings.TrimSpace(dep)
		if strings.HasSuffix(dep, "/sdk/go/db") || strings.Contains(dep, "/sdk/go/internal/") {
			t.Errorf("jobs/def depends on %s", dep)
		}
	}
}

func TestDefineCron_AppliesCronDefaults(t *testing.T) {
	d := DefineCron("sales_expire_quotations", Schedule("0 3 * * *"), Label("Expire"))

	want := Spec{Label: "Expire", Schedule: "0 3 * * *", Queue: QueueBulk, Timeout: time.Hour}
	if d.Name() != "sales_expire_quotations" || d.Spec() != want {
		t.Errorf("Name/Spec = %q/%+v, want %+v", d.Name(), d.Spec(), want)
	}
}

func TestDefineCron_KeepsExplicitOptions(t *testing.T) {
	d := DefineCron("weekly_scan", Schedule("0 3 * * 0"), Label("Scan"), Description("Scans"),
		Queue(QueueSearch), Timeout(2*time.Hour), DisabledByDefault())

	want := Spec{
		Label: "Scan", Description: "Scans", Schedule: "0 3 * * 0", Queue: QueueSearch, Timeout: 2 * time.Hour,
		DisabledByDefault: true,
	}
	if d.Spec() != want {
		t.Errorf("Spec = %+v, want %+v", d.Spec(), want)
	}
}

func TestDefineCron_PanicsOnInvalidDefinition(t *testing.T) {
	label, schedule := Label("L"), Schedule("* * * * *")
	mustPanic(t, "not snake_case", func() { DefineCron("Bad-Name", label, schedule) })
	mustPanic(t, "missing label", func() { DefineCron("cron", schedule) })
	mustPanic(t, "missing schedule", func() { DefineCron("cron", label) })
	mustPanic(t, "unknown queue", func() { DefineCron("cron", label, schedule, Queue("urgent")) })
	mustPanic(t, "timeout over 24h", func() { DefineCron("cron", label, schedule, Timeout(25*time.Hour)) })
	mustPanic(t, "job-only MaxAttempts", func() { DefineCron("cron", label, schedule, MaxAttempts(3)) })
	mustPanic(t, "job-only Priority", func() { DefineCron("cron", label, schedule, Priority(50)) })
	mustPanic(t, "job-only UniqueBy", func() { DefineCron("cron", label, schedule, UniqueBy("id")) })
}

func TestDefine_RejectsCronOnlyOptions(t *testing.T) {
	label := Label("L")
	mustPanic(t, "Schedule", func() { Define[struct{}]("job", label, Schedule("* * * * *")) })
	mustPanic(t, "DisabledByDefault", func() { Define[struct{}]("job", label, DisabledByDefault()) })
}

func TestValidateSchedule(t *testing.T) {
	valid := []string{
		"* * * * *", "0 3 * * *", "*/5 * * * *", "0 0 1 1 *", "0 9-17 * * 1-5", "15,45 * * * *",
		"0 0 * * 0", "0 0 * * 7", "0 0 * jan mon-fri", "0-30/10 * * * *", "0  3   * * *",
	}
	for _, expr := range valid {
		if err := validateSchedule(expr); err != nil {
			t.Errorf("validateSchedule(%q) = %v, want nil", expr, err)
		}
	}

	invalid := []string{
		"", "* * * *", "* * * * * *", "@daily", "60 * * * *", "* 24 * * *", "* * 0 * *", "* * 32 * *",
		"* * * 13 *", "* * * * 8", "a * * * *", "*/0 * * * *", "*/x * * * *", "5-1 * * * *", "1- * * * *",
		",1 * * * *", "0 0 * foo *", "+5 * * * *", "*/+5 * * * *", "-1 * * * *",
	}
	for _, expr := range invalid {
		if err := validateSchedule(expr); err == nil {
			t.Errorf("validateSchedule(%q) = nil, want an error", expr)
		}
	}
}
