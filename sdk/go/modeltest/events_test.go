package modeltest

import (
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
	"github.com/djangbahevans/goerp/sdk/go/events"
	"github.com/vmihailenco/msgpack/v5"
)

type eventProbePayload struct {
	Reference string   `msgpack:"reference"`
	Count     int64    `msgpack:"count"`
	Labels    []string `msgpack:"labels"`
	Note      *string  `msgpack:"note"`
}

var (
	emittedV1   = events.Define[eventProbePayload]("asyncprobe.output.recorded")
	emittedV2   = events.Define[string]("asyncprobe.output.recorded", events.Version(2))
	emittedV3   = events.Define[eventProbePayload]("asyncprobe.output.recorded", events.Version(3))
	emittedList = events.Define[[]int64]("asyncprobe.output.listed")
)

func eventArgs(t *testing.T, h *Harness, def events.Definition, payload any) jobqueue.EventDeliveryArgs {
	t.Helper()

	encoded, err := msgpack.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}

	return jobqueue.EventDeliveryArgs{
		EventID:       uuid.New().String(),
		EventName:     def.Name(),
		EventVersion:  def.Version(),
		EmitterModule: h.module.Manifest.Name,
		TenantID:      h.TenantID,
		UserID:        h.UserID,
		Payload:       encoded,
		EmittedAt:     time.Date(2040, 1, 2, 3, 4, 5, 0, time.UTC),
	}
}

func insertEvent(t *testing.T, h *Harness, args jobqueue.EventDeliveryArgs) {
	t.Helper()

	if _, err := h.runtime.EventInsertClient().Insert(t.Context(), args, nil); err != nil {
		t.Fatal(err)
	}
}

func TestEvents_MatchVersionsAndDecodeTypedPayloads(t *testing.T) {
	h := asyncHarness(t)
	first := eventProbePayload{Reference: "first"}
	last := eventProbePayload{
		Reference: "last",
		Count:     9007199254740993,
		Labels:    []string{"GH", "KE"},
		Note:      new("optional"),
	}
	insertEvent(t, h, eventArgs(t, h, emittedV1, first))
	insertEvent(t, h, eventArgs(t, h, emittedList, []int64{42, 9007199254740993}))
	args := eventArgs(t, h, emittedV1, last)
	args.Transactional = true
	args.SyncDispatched = true
	insertEvent(t, h, args)
	insertEvent(t, h, eventArgs(t, h, emittedV2, "version-two"))

	h.Events.AssertEmitted(emittedV1)
	h.Events.AssertEmitted(emittedV2)
	h.Events.AssertNotEmitted(emittedV3)
	h.Events.AssertEmittedN(emittedV1, 2)
	h.Events.AssertEmittedN(emittedV2, 1)
	h.Events.AssertEmittedInOrder(emittedV1, emittedV1, emittedV2)
	h.Events.AssertEmittedInOrder(emittedV1, emittedList, emittedV1, emittedV2)

	got := h.Events.Last(emittedV1)
	want := Event[eventProbePayload]{
		ID:               args.EventID,
		Name:             emittedV1.Name(),
		Version:          1,
		EmitterModule:    "asyncprobe",
		Payload:          last,
		WasTransactional: true,
		SyncDispatched:   true,
		EmittedAt:        args.EmittedAt,
	}
	if !reflect.DeepEqual(*got, want) {
		t.Fatalf("typed emission = %+v, want %+v", got, want)
	}
	if got := h.Events.Last(emittedV2).Payload; got != "version-two" {
		t.Fatalf("scalar payload = %q", got)
	}
	if got := h.Events.Last(emittedList).Payload; !reflect.DeepEqual(got, []int64{42, 9007199254740993}) {
		t.Fatalf("list payload = %v", got)
	}

	assertions := *h.Events
	assertions.t = panicFailer{t}
	for name, check := range map[string]struct {
		assert  func()
		version string
	}{
		"absent version": {
			assert:  func() { assertions.AssertEmitted(emittedV3) },
			version: "v3",
		},
		"count": {
			assert:  func() { assertions.AssertEmittedN(emittedV2, 2) },
			version: "v2",
		},
		"not emitted": {
			assert:  func() { assertions.AssertNotEmitted(emittedV1) },
			version: "v1",
		},
		"last": {
			assert:  func() { assertions.Last(emittedV3) },
			version: "v3",
		},
		"order": {
			assert:  func() { assertions.AssertEmittedInOrder(emittedV2, emittedV1) },
			version: "v1",
		},
		"repeated": {
			assert:  func() { assertions.AssertEmittedInOrder(emittedV1, emittedV1, emittedV1) },
			version: "v1",
		},
	} {
		if message := failure(check.assert); !strings.Contains(message, check.version) {
			t.Errorf("%s assertion failed with %q, want version %s", name, message, check.version)
		}
	}
}

func TestEvents_OnlyCommittedRowsForTheHarnessTenantAreVisible(t *testing.T) {
	h := asyncHarness(t)
	args := eventArgs(t, h, emittedV1, eventProbePayload{Reference: "foreign"})
	args.TenantID = uuid.New().String()
	insertEvent(t, h, args)
	t.Cleanup(func() {
		if _, err := h.DB.db.Exec("DELETE FROM system.river_job WHERE args->>'tenant_id' = $1", args.TenantID); err != nil {
			t.Error(err)
		}
	})
	h.Events.AssertNotEmitted(emittedV1)

	for _, commit := range []bool{false, true} {
		tx, err := h.DB.db.BeginTx(t.Context(), nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = tx.Rollback() })

		args := eventArgs(t, h, emittedV1, eventProbePayload{Reference: "transaction"})
		args.Transactional = true
		if _, err := h.runtime.EventInsertClient().InsertTx(t.Context(), tx, args, nil); err != nil {
			t.Fatal(err)
		}
		h.Events.AssertNotEmitted(emittedV1)

		if commit {
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
		} else if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
	}

	h.Events.AssertEmittedN(emittedV1, 1)
	got := h.Events.Last(emittedV1)
	if !got.WasTransactional || got.Payload.Reference != "transaction" {
		t.Fatalf("committed event = %+v", got)
	}
}

func TestEvents_LastFailsForUndecodableMatchingPayload(t *testing.T) {
	h := asyncHarness(t)
	args := eventArgs(t, h, emittedV2, "valid")
	args.Payload = []byte{0xc1}
	insertEvent(t, h, args)
	insertEvent(t, h, eventArgs(t, h, emittedV1, eventProbePayload{Reference: "valid"}))

	h.Events.AssertEmitted(emittedV2)
	if got := h.Events.Last(emittedV1).Payload.Reference; got != "valid" {
		t.Fatalf("unrelated malformed payload affected Last: %q", got)
	}
	assertions := *h.Events
	assertions.t = panicFailer{t}
	message := failure(func() { assertions.Last(emittedV2) })
	if !strings.Contains(message, "decode event") || !strings.Contains(message, "v2") {
		t.Fatalf("malformed payload failure = %q", message)
	}
}

func TestEvents_RejectStringsAndPreservePayloadTypesAtCompileTime(t *testing.T) {
	for name, call := range map[string]string{
		"AssertEmitted":        `h.Events.AssertEmitted("event.name")`,
		"AssertNotEmitted":     `h.Events.AssertNotEmitted("event.name")`,
		"AssertEmittedN":       `h.Events.AssertEmittedN("event.name", 1)`,
		"AssertEmittedInOrder": `h.Events.AssertEmittedInOrder("event.name")`,
		"Last":                 `h.Events.Last[string]("event.name")`,
		"Payload":              `var payload bool = h.Events.Last(fixture.Received).Payload; _ = payload`,
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			source := fmt.Sprintf(`package main
import (
 "github.com/djangbahevans/goerp/sdk/go/modeltest"
 fixture "github.com/djangbahevans/goerp/sdk/go/modeltest/testdata/asyncfixture"
)
func main() { var h *modeltest.Harness; _ = fixture.Received; %s }
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
