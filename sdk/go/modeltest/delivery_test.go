package modeltest

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
	"uuid"

	"github.com/djangbahevans/goerp/internal/engine/jobqueue"
	"github.com/djangbahevans/goerp/internal/engine/user"
	"github.com/djangbahevans/goerp/sdk/go/events"
	"github.com/djangbahevans/goerp/sdk/go/jobs"
	fixture "github.com/djangbahevans/goerp/sdk/go/modeltest/testdata/asyncfixture"
)

var unusedJob = jobs.Define[fixture.Payload]("unused", jobs.Label("Unused job"))

func asyncHarness(t *testing.T, opts ...Option) *Harness {
	t.Helper()
	t.Chdir("testdata/asyncfixture")
	return NewHarness(t, opts...)
}

func TestHarness_ConfigEventJobFlow(t *testing.T) {
	h := asyncHarness(t, WithConfig(fixture.Country, "KE"))

	if err := h.Deliver(fixture.Received, fixture.Payload{Reference: "order-42"}); err != nil {
		t.Fatal(err)
	}

	h.DB.AssertExists("effects", map[string]any{
		"reference":    "order-42",
		"source":       "event",
		"country":      "KE",
		"tenant_seen":  h.TenantID,
		"user_seen":    h.UserID,
		"contact_seen": h.UserID,
		"roles_seen":   "admin",
	})

	h.Jobs.AssertEnqueued(fixture.Process)
	h.Jobs.AssertEnqueuedCount(fixture.Process, 1)
	h.Jobs.AssertNotEnqueued(unusedJob)
	last := h.Jobs.Last(fixture.Process)
	if last.Type != fixture.Process.Name() || last.ID == "" || last.Queue != "bulk" || last.MaxAttempts != 5 || last.Payload["reference"] != "order-42" {
		t.Fatalf("queued job = %+v", last)
	}

	assertions := *h.Jobs
	assertions.t = panicFailer{t}
	for name, assertWrong := range map[string]func(){
		"absent":       func() { assertions.AssertEnqueued(unusedJob) },
		"count":        func() { assertions.AssertEnqueuedCount(fixture.Process, 2) },
		"not enqueued": func() { assertions.AssertNotEnqueued(fixture.Process) },
		"last":         func() { assertions.Last(unusedJob) },
	} {
		if message := failure(assertWrong); message == "" {
			t.Errorf("%s assertion accepted the wrong jobs", name)
		}
	}
	if _, err := h.runtime.EventInsertClient().Insert(t.Context(), jobqueue.WASMJobArgs{
		ModuleName: "other_module",
		TenantID:   h.TenantID,
		JobType:    fixture.Process.Name(),
	}, nil); err != nil {
		t.Fatal(err)
	}
	h.Jobs.AssertEnqueuedCount(fixture.Process, 1)

	SetConfig(h, fixture.Country, "NG")
	if err := h.RunJob(fixture.Process, fixture.Payload{Reference: "order-42"}); err != nil {
		t.Fatal(err)
	}
	h.DB.AssertExists("effects", map[string]any{
		"reference":    "order-42",
		"source":       "job",
		"country":      "NG",
		"attempt":      1,
		"tenant_seen":  h.TenantID,
		"user_seen":    h.UserID,
		"type_seen":    fixture.Process.Name(),
		"contact_seen": h.UserID,
		"roles_seen":   "admin",
	})
	h.Jobs.AssertEnqueuedCount(fixture.Process, 1)
}

func TestDeliver_VersionsDuplicateIDsAndPermanentDecodeFailure(t *testing.T) {
	h := asyncHarness(t)
	id := uuid.New().String()
	for range 2 {
		if err := h.Deliver(fixture.Received, fixture.Payload{Reference: "plain"}, WithEventID(id)); err != nil {
			t.Fatal(err)
		}
	}
	h.DB.AssertCount("effects", 2, "source = 'event'")
	h.Jobs.AssertEnqueuedCount(fixture.Process, 2)
	if err := h.Deliver(fixture.ReceivedV2, fixture.Payload{Reference: "v2"}); err != nil {
		t.Fatal(err)
	}
	h.DB.AssertExists("effects", map[string]any{"reference": "v2", "source": "event-v2"})

	err := h.DeliverRaw(fixture.Received, []byte{0xc1})
	if _, ok := errors.AsType[*events.PermanentDeliveryError](err); !ok {
		t.Fatalf("undecodable payload = %v, want permanent error", err)
	}
	h.DB.AssertCount("effects", 3, "TRUE")
	missing := events.Define[fixture.Payload](fixture.Received.Name(), events.Version(3))
	if err := h.Deliver(missing, fixture.Payload{}); err == nil {
		t.Fatal("unregistered version succeeded")
	}
	if err := h.Deliver(fixture.Received, fixture.Payload{}, WithEventID("")); err == nil {
		t.Fatal("empty event ID succeeded")
	}
}

func TestDeliver_TransactionalCommitDuplicateSkipAndRollback(t *testing.T) {
	h := asyncHarness(t)
	id := uuid.New().String()
	for range 2 {
		if err := h.Deliver(fixture.Committed, fixture.Payload{Reference: "committed"}, WithEventID(id)); err != nil {
			t.Fatal(err)
		}
	}
	h.DB.AssertCount("effects", 1, "TRUE")
	h.DB.AssertCount("event_deliveries", 1, "TRUE")
	h.Jobs.AssertEnqueuedCount(fixture.Process, 1)

	for _, mode := range []string{"retry", "permanent"} {
		t.Run(mode, func(t *testing.T) {
			id := uuid.New().String()
			err := h.Deliver(fixture.Committed, fixture.Payload{Reference: mode, Mode: mode}, WithEventID(id))
			if err == nil {
				t.Fatal("failed handler returned success")
			}
			_, permanent := errors.AsType[*events.PermanentDeliveryError](err)
			if permanent != (mode == "permanent") {
				t.Fatalf("mode %s returned %v", mode, err)
			}
			h.DB.AssertNotExists("effects", map[string]any{"reference": mode})
			h.DB.AssertNotExists("event_deliveries", map[string]any{"event_id": id})
			if err := h.Deliver(fixture.Committed, fixture.Payload{Reference: mode}, WithEventID(id)); err != nil {
				t.Fatalf("retry after rollback: %v", err)
			}
		})
	}
	h.Jobs.AssertEnqueuedCount(fixture.Process, 3)
}

func TestRunJob_AttemptsFailuresAndLiveIdentity(t *testing.T) {
	h := asyncHarness(t)
	if err := h.RunJob(fixture.Process, fixture.Payload{Reference: "attempt"}, WithJobAttempt(4)); err != nil {
		t.Fatal(err)
	}
	h.DB.AssertExists("effects", map[string]any{"reference": "attempt", "attempt": 4})
	for _, mode := range []string{"retry", "permanent"} {
		err := h.RunJob(fixture.Process, fixture.Payload{Reference: mode, Mode: mode})
		if err == nil || jobs.IsPermanentError(err) != (mode == "permanent") {
			t.Fatalf("mode %s returned %v", mode, err)
		}
	}
	if err := h.RunJob(unusedJob, fixture.Payload{}); err == nil {
		t.Fatal("unregistered handler succeeded")
	}
	if err := h.RunJob(fixture.Process, fixture.Payload{}, WithJobAttempt(0)); err == nil {
		t.Fatal("zero attempt succeeded")
	}

	if _, err := h.DB.db.ExecContext(t.Context(), "UPDATE "+h.DB.schema()+".tenant_members SET status = 'suspended' WHERE user_id = $1", h.UserID); err != nil {
		t.Fatal(err)
	}
	if err := h.RunJob(fixture.Process, fixture.Payload{}); err == nil {
		t.Fatal("suspended member ran job")
	}
	if err := h.Deliver(fixture.Received, fixture.Payload{}); err == nil {
		t.Fatal("suspended member received event")
	}
	h.DB.AssertCount("effects", 1, "TRUE")
}

func TestRunJob_DeclaredTimeoutCancelsGuest(t *testing.T) {
	h := asyncHarness(t)
	h.module.Manifest.JobTypes[0].TimeoutSeconds = 1

	started := time.Now()
	err := h.RunJob(fixture.Process, fixture.Payload{Mode: "sleep"})
	if err == nil || time.Since(started) > 5*time.Second {
		t.Fatalf("sleeping job timeout: %v, elapsed %s", err, time.Since(started))
	}

	h.module.Manifest.JobTypes[0].TimeoutSeconds = 10
	if err := h.RunJob(fixture.Process, fixture.Payload{Reference: "after-timeout"}); err != nil {
		t.Fatal(err)
	}
	h.DB.AssertExists("effects", map[string]any{"reference": "after-timeout"})
}

func TestSetConfig_TypesGuestWritesAndTenantIsolation(t *testing.T) {
	h := asyncHarness(t, WithConfig(fixture.Country, "KE"))
	other := NewHarness(t, WithConfig(fixture.Country, "NG"))
	SetConfig(h, fixture.Enabled, false)
	SetConfig(h, fixture.Count, 9007199254740993)
	SetConfig(h, fixture.Ratio, 0.75)
	SetConfig(h, fixture.Counts, []int{9007199254740993})
	SetConfig(h, fixture.Ratios, []float64{0.75})
	SetConfig(h, fixture.Interval, 2*time.Minute)
	SetConfig(h, fixture.Countries, []string{"KE", "GH"})
	SetConfig(h, fixture.SettingsValue, fixture.Settings{Country: "US"})
	SetConfig(h, fixture.Secret, "fixture-secret")

	response := h.GET("/asyncprobe/config")
	if response.StatusCode != 200 {
		t.Fatalf("config response = %d %s", response.StatusCode, response.body)
	}
	type configValues struct {
		Country   string           `json:"country"`
		Enabled   bool             `json:"enabled"`
		Count     int64            `json:"count"`
		Ratio     float64          `json:"ratio"`
		Counts    []int64          `json:"counts"`
		Ratios    []float64        `json:"ratios"`
		Interval  string           `json:"interval"`
		Countries []string         `json:"countries"`
		Settings  fixture.Settings `json:"settings"`
		Secret    string           `json:"secret"`
	}
	var values configValues
	response.ParseJSON(&values)
	want := configValues{
		Country:   "KE",
		Enabled:   false,
		Count:     9007199254740993,
		Ratio:     0.75,
		Interval:  "2m0s",
		Countries: []string{"KE", "GH"},
		Counts:    []int64{9007199254740993},
		Ratios:    []float64{0.75},
		Settings:  fixture.Settings{Country: "US"},
		Secret:    "fixture-secret",
	}
	if !reflect.DeepEqual(values, want) {
		t.Fatalf("config values = %+v, want %+v", values, want)
	}

	if got := other.GET("/asyncprobe/config").JSON("country"); got != "NG" {
		t.Fatalf("other tenant country = %v", got)
	}

	if response := h.POST("/asyncprobe/config", nil, WithQuery("country", "TZ")); response.StatusCode != 200 {
		t.Fatalf("guest write: %d %s", response.StatusCode, response.body)
	}
	if got := h.GET("/asyncprobe/config").JSON("country"); got != "TZ" {
		t.Fatalf("guest write country = %v", got)
	}
	SetConfig(h, fixture.Country, "ZA")
	if got := h.GET("/asyncprobe/config").JSON("country"); got != "ZA" {
		t.Fatalf("updated fixture country = %v", got)
	}
}

func TestHarness_ConcurrentTenantIsolation(t *testing.T) {
	h := asyncHarness(t, WithConfig(fixture.Country, "KE"))
	other := NewHarness(t, WithConfig(fixture.Country, "NG"))
	var wg sync.WaitGroup
	errs := make(chan error, 4)
	for _, harness := range []*Harness{h, other} {
		wg.Go(func() { errs <- harness.Deliver(fixture.Received, fixture.Payload{Reference: "shared"}) })
		wg.Go(func() { errs <- harness.RunJob(fixture.Process, fixture.Payload{Reference: "shared"}) })
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	for i, harness := range []*Harness{h, other} {
		harness.DB.AssertCount("effects", 2, "TRUE")
		harness.DB.AssertExists("effects", map[string]any{"country": []string{"KE", "NG"}[i], "tenant_seen": harness.TenantID})
		harness.Jobs.AssertEnqueuedCount(fixture.Process, 1)
	}
}

func TestHarness_WithUserPreservesAccountAndCleansTenant(t *testing.T) {
	t.Chdir("testdata/asyncfixture")
	db := openTestPrimaryDB(t)
	if err := user.NewStore(db).Bootstrap(t.Context()); err != nil {
		t.Fatal(err)
	}
	userID := uuid.New().String()
	if _, err := db.ExecContext(t.Context(), "INSERT INTO system.users (id, email) VALUES ($1, $2)", userID, userID+"@existing.invalid"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := db.ExecContext(context.Background(), "DELETE FROM system.users WHERE id = $1", userID); err != nil {
			t.Errorf("delete account fixture: %v", err)
		}
	})

	var tenantID, schema string
	t.Run("tenant", func(t *testing.T) {
		h := NewHarness(t, WithUser(userID, nil), WithConfig(fixture.Country, "KE"))
		tenantID, schema = h.TenantID, "tenant_"+h.tenantSlug
		if err := h.Deliver(fixture.Received, fixture.Payload{Reference: "existing-user"}); err != nil {
			t.Fatal(err)
		}
		h.DB.AssertExists("effects", map[string]any{
			"user_seen":    userID,
			"contact_seen": userID,
			"roles_seen":   "user",
		})
	})

	var email string
	if err := db.QueryRowContext(t.Context(), "SELECT email FROM system.users WHERE id = $1", userID).Scan(&email); err != nil {
		t.Fatal(err)
	}
	if email != userID+"@existing.invalid" {
		t.Fatalf("existing account changed: %s", email)
	}
	var tenants, jobs, schemas int
	if err := db.QueryRowContext(t.Context(), "SELECT count(*) FROM system.tenants WHERE id = $1", tenantID).Scan(&tenants); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(t.Context(), "SELECT count(*) FROM system.river_job WHERE args->>'tenant_id' = $1", tenantID).Scan(&jobs); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(t.Context(), "SELECT count(*) FROM information_schema.schemata WHERE schema_name = $1", schema).Scan(&schemas); err != nil {
		t.Fatal(err)
	}
	if tenants != 0 || jobs != 0 || schemas != 0 {
		t.Fatalf("cleanup left tenants=%d jobs=%d schemas=%d", tenants, jobs, schemas)
	}
}

func TestHarness_TypedInputsRejectMismatchedPayloadsAtCompileTime(t *testing.T) {
	for name, call := range map[string]string{
		"SetConfig":  "modeltest.SetConfig(h, fixture.Country, true)",
		"WithConfig": "_ = modeltest.WithConfig(fixture.Country, true)",
		"Deliver":    "_ = h.Deliver(fixture.Received, true)",
		"RunJob":     "_ = h.RunJob(fixture.Process, true)",
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			source := fmt.Sprintf(`package main
import (
 "github.com/djangbahevans/goerp/sdk/go/modeltest"
 fixture "github.com/djangbahevans/goerp/sdk/go/modeltest/testdata/asyncfixture"
)
func main() { var h *modeltest.Harness; _ = h; %s }
`, call)
			file := filepath.Join(dir, "main.go")
			if err := os.WriteFile(file, []byte(source), 0o600); err != nil {
				t.Fatal(err)
			}
			cmd := exec.CommandContext(t.Context(), "go", "build", "-o", filepath.Join(dir, "bad"), file)
			output, err := cmd.CombinedOutput()
			if err == nil || !strings.Contains(string(output), "cannot use true") {
				t.Fatalf("wrong type build: %v\n%s", err, output)
			}
		})
	}
}
