package modeltest

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
	"uuid"

	"github.com/djangbahevans/goerp/internal/engine/jobqueue"
	"github.com/djangbahevans/goerp/sdk/go/jobs"
	fixture "github.com/djangbahevans/goerp/sdk/go/modeltest/testdata/asyncfixture"
	"github.com/riverqueue/river"
	"github.com/vmihailenco/msgpack/v5"
)

type jobProbePayload struct {
	Reference string   `msgpack:"reference"`
	Count     int64    `msgpack:"count"`
	Labels    []string `msgpack:"labels"`
	Note      *string  `msgpack:"note"`
}

func jobArgs(t *testing.T, h *Harness, def jobs.Definition, payload any) jobqueue.WASMJobArgs {
	t.Helper()

	encoded, err := msgpack.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}

	return jobqueue.WASMJobArgs{
		ModuleName:  h.module.Manifest.Name,
		TenantID:    h.TenantID,
		UserID:      h.UserID,
		JobType:     def.Name(),
		Payload:     encoded,
		Queue:       jobs.QueueBulk,
		MaxAttempts: 7,
	}
}

func (h *Harness) assertJobPayload[P any](t *testing.T, name string, payload P) {
	t.Helper()

	def := jobs.Define[P](name, jobs.Label(name))
	args := jobArgs(t, h, def, payload)
	scheduledAt := time.Date(2040, 1, 2, 3, 4, 5, 0, time.UTC)
	inserted, err := h.runtime.EventInsertClient().Insert(t.Context(), args, &river.InsertOpts{
		ScheduledAt: scheduledAt,
	})
	if err != nil {
		t.Fatal(err)
	}

	h.Jobs.AssertEnqueued(def)
	h.Jobs.AssertEnqueuedCount(def, 1)
	got := h.Jobs.Last(def)
	if !reflect.DeepEqual(got.Payload, payload) {
		t.Fatalf("typed job %s payload = %+v, want %+v", name, got.Payload, payload)
	}
	if got.ID != jobqueue.EncodeJobID(inserted.Job.ID) || got.Type != def.Name() ||
		got.Queue != jobs.QueueBulk || got.MaxAttempts != 7 ||
		!got.ScheduledAt.Equal(scheduledAt) || !bytes.Equal(got.RawPayload, args.Payload) {
		t.Fatalf("job %s metadata = %+v", name, got)
	}
}

func TestJobs_LastPreservesPayloadTypesAndMetadata(t *testing.T) {
	h := asyncHarness(t)
	payload := jobProbePayload{
		Reference: "order-42",
		Count:     9007199254740993,
		Labels:    []string{"GH", "KE"},
		Note:      new("optional"),
	}

	h.assertJobPayload(t, "struct_payload", payload)
	h.assertJobPayload(t, "string_payload", "scalar")
	h.assertJobPayload(t, "integer_payload", int64(9007199254740993))
	h.assertJobPayload(t, "slice_payload", []int64{42, 9007199254740993})
	h.assertJobPayload(t, "pointer_payload", new(payload))
	h.assertJobPayload(t, "map_payload", map[string]int64{"count": 9007199254740993})
	h.assertJobPayload(t, "nil_pointer_payload", (*jobProbePayload)(nil))
	h.assertJobPayload(t, "nil_slice_payload", []int64(nil))
	h.assertJobPayload(t, "nil_map_payload", map[string]int64(nil))
}

func TestJobs_LastDecodesOnlyTheMostRecentMatchingEnqueue(t *testing.T) {
	h := asyncHarness(t)
	def := fixture.Process
	args := jobArgs(t, h, def, fixture.Payload{Reference: "first"})
	args.Payload = []byte{0xc1}
	if _, err := h.runtime.EventInsertClient().Insert(t.Context(), args, nil); err != nil {
		t.Fatal(err)
	}
	if err := h.Deliver(fixture.Received, fixture.Payload{Reference: "last"}); err != nil {
		t.Fatal(err)
	}
	args.JobType = unusedJob.Name()
	if _, err := h.runtime.EventInsertClient().Insert(t.Context(), args, nil); err != nil {
		t.Fatal(err)
	}

	h.Jobs.AssertEnqueuedCount(def, 2)
	if got := h.Jobs.Last(def).Payload.Reference; got != "last" {
		t.Fatalf("most recent payload = %q, want last", got)
	}

	assertions := *h.Jobs
	assertions.t = panicFailer{t}
	message := failure(func() { assertions.Last(unusedJob) })
	if !strings.Contains(message, "decode job") || !strings.Contains(message, unusedJob.Name()) {
		t.Fatalf("malformed payload failure = %q", message)
	}

	for _, raw := range [][]byte{nil, {0xc1}, {0xa1, 'x'}} {
		args.JobType = def.Name()
		args.Payload = raw
		if _, err := h.runtime.EventInsertClient().Insert(t.Context(), args, nil); err != nil {
			t.Fatal(err)
		}

		message := failure(func() { assertions.Last(def) })
		if !strings.Contains(message, "decode job") || !strings.Contains(message, def.Name()) {
			t.Fatalf("payload %x failure = %q", raw, message)
		}
	}
}

func TestJobs_OnlyCommittedRowsForTheHarnessTenantAndModuleAreVisible(t *testing.T) {
	h := asyncHarness(t)
	def := fixture.Process
	foreign := jobArgs(t, h, def, fixture.Payload{Reference: "foreign"})
	foreignTenantID := uuid.New().String()
	foreign.TenantID = foreignTenantID
	if _, err := h.runtime.EventInsertClient().Insert(t.Context(), foreign, nil); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := h.DB.db.Exec("DELETE FROM system.river_job WHERE args->>'tenant_id' = $1", foreignTenantID); err != nil {
			t.Error(err)
		}
	})
	foreign.TenantID = h.TenantID
	foreign.ModuleName = "other_module"
	if _, err := h.runtime.EventInsertClient().Insert(t.Context(), foreign, nil); err != nil {
		t.Fatal(err)
	}

	h.Jobs.AssertNotEnqueued(def)
	assertions := *h.Jobs
	assertions.t = panicFailer{t}
	message := failure(func() { assertions.Last(def) })
	if !strings.Contains(message, "expected job") || !strings.Contains(message, def.Name()) {
		t.Fatalf("missing job failure = %q", message)
	}
	if message := failure(func() { assertions.Last(jobs.Def[fixture.Payload]{}) }); !strings.Contains(message, "definition must not be empty") {
		t.Fatalf("empty definition failure = %q", message)
	}

	for _, commit := range []bool{false, true} {
		tx, err := h.DB.db.BeginTx(t.Context(), nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = tx.Rollback() })

		args := jobArgs(t, h, def, fixture.Payload{Reference: "transaction"})
		if _, err := h.runtime.EventInsertClient().InsertTx(t.Context(), tx, args, nil); err != nil {
			t.Fatal(err)
		}
		h.Jobs.AssertNotEnqueued(def)

		if commit {
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
		} else if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
	}

	h.Jobs.AssertEnqueuedCount(def, 1)
	got := h.Jobs.Last(def)
	if got.Payload.Reference != "transaction" {
		t.Fatalf("committed payload = %+v", got.Payload)
	}

	var state string
	var attempt int
	id, err := jobqueue.DecodeJobID(got.ID)
	if err != nil {
		t.Fatal(err)
	}

	if err := h.DB.db.QueryRowContext(t.Context(), `SELECT state, attempt FROM system.river_job WHERE id = $1`, id).Scan(&state, &attempt); err != nil {
		t.Fatal(err)
	}
	if state != "available" || attempt != 0 {
		t.Fatalf("assertions changed queued job: state=%s attempt=%d", state, attempt)
	}
}

func TestJobs_RejectStringsAndPreservePayloadTypesAtCompileTime(t *testing.T) {
	for name, call := range map[string]string{
		"AssertEnqueued":      `h.Jobs.AssertEnqueued("job_name")`,
		"AssertNotEnqueued":   `h.Jobs.AssertNotEnqueued("job_name")`,
		"AssertEnqueuedCount": `h.Jobs.AssertEnqueuedCount("job_name", 1)`,
		"Last":                `h.Jobs.Last[string]("job_name")`,
		"Payload":             `var payload bool = h.Jobs.Last(fixture.Process).Payload; _ = payload`,
		"Job":                 `var job *modeltest.Job[bool] = h.Jobs.Last(fixture.Process); _ = job`,
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			source := fmt.Sprintf(`package main
import (
 "github.com/djangbahevans/goerp/sdk/go/modeltest"
 fixture "github.com/djangbahevans/goerp/sdk/go/modeltest/testdata/asyncfixture"
)
func main() { var h *modeltest.Harness; _ = fixture.Process; %s }
`, call)
			file := filepath.Join(dir, "main.go")
			if err := os.WriteFile(file, []byte(source), 0o600); err != nil {
				t.Fatal(err)
			}

			cmd := exec.CommandContext(t.Context(), "go", "build", "-o", filepath.Join(dir, "bad"), file)
			output, err := cmd.CombinedOutput()
			if err == nil || !strings.Contains(string(output), "cannot use") {
				t.Fatalf("wrong type build: %v\n%s", err, output)
			}
		})
	}
}
