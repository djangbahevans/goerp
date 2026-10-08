package wasm

import (
	"context"
	"encoding/json/v2"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/config"
	"github.com/djangbahevans/goerp/internal/engine/wasm/wasmtest"
	"github.com/tetratelabs/wazero"
	"github.com/vmihailenco/msgpack/v5"
)

type guestClockSample struct {
	InitializedAt time.Time           `json:"initialized_at"`
	Before        time.Time           `json:"before"`
	After         time.Time           `json:"after"`
	RequestedAt   time.Time           `json:"requested_at"`
	ElapsedNS     int64               `json:"elapsed_ns"`
	Random        string              `json:"random"`
	LibraryValid  bool                `json:"library_valid"`
	HostTime      abiv1.TimeNowOutput `json:"host_time"`
}

func clockFixtureRuntime(t *testing.T, now func() time.Time) (*Runtime, wazero.CompiledModule) {
	t.Helper()

	path := filepath.Join(t.TempDir(), "clockfixture.wasm")
	cmd := exec.CommandContext(t.Context(), "go", "build", "-buildmode=c-shared", "-o", path, "../../../sdk/go/modeltest/testdata/clockfixture/cmd/module")
	cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build clock fixture: %v\n%s", err, out)
	}

	binary, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read clock fixture: %v", err)
	}

	rt, err := New(&config.Config{
		Environment:       string(config.Production),
		PoolMaxMemoryByes: 64 << 20,
		CompilationCache:  wasmtest.SharedCompilationCacheDir(),
	}, nil, nil, nil, WithClock(now))
	if err != nil {
		t.Fatalf("create runtime: %v", err)
	}
	t.Cleanup(func() { _ = rt.Close(context.Background()) })

	compiled, err := rt.CompileModule(t.Context(), binary)
	if err != nil {
		t.Fatalf("compile clock fixture: %v", err)
	}
	t.Cleanup(func() { _ = compiled.Close(context.Background()) })

	return rt, compiled
}

func readGuestClockSample(t *testing.T, ctx context.Context, rt *Runtime, inst *ModuleInstance) guestClockSample {
	t.Helper()

	payload, err := msgpack.Marshal(abiv1.Request{
		Method:      "GET",
		Path:        "/sample",
		RequestedAt: rt.invocationTime(ctx),
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	response, err := inst.InvokeHandleRequest(ctx, payload)
	if err != nil {
		t.Fatalf("invoke request: %v", err)
	}

	var wire abiv1.Response
	if err := msgpack.Unmarshal(response, &wire); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if wire.StatusCode != 200 {
		t.Fatalf("status = %d, body = %s", wire.StatusCode, wire.Body)
	}

	var sample guestClockSample
	if err := json.Unmarshal(wire.Body, &sample); err != nil {
		t.Fatalf("decode sample: %v", err)
	}

	return sample
}

func assertGuestClockSample(t *testing.T, sample guestClockSample, initializedAt, acceptedAt time.Time) {
	t.Helper()

	if !sample.InitializedAt.Equal(initializedAt) {
		t.Errorf("init time = %v, want %v", sample.InitializedAt, initializedAt)
	}

	if !sample.Before.Equal(acceptedAt) || !sample.After.Equal(acceptedAt) || !sample.RequestedAt.Equal(acceptedAt) {
		t.Errorf("request times = %v / %v / %v, want %v", sample.Before, sample.After, sample.RequestedAt, acceptedAt)
	}

	if sample.ElapsedNS < int64(25*time.Millisecond) {
		t.Errorf("elapsed = %v, want real sleep duration", time.Duration(sample.ElapsedNS))
	}

	if !sample.LibraryValid {
		t.Error("JWT library did not observe the injected clock")
	}

	if sample.HostTime.UnixMs != acceptedAt.UnixMilli() || sample.HostTime.ISO8601 != acceptedAt.UTC().Format(time.RFC3339Nano) {
		t.Errorf("host.time.now = %+v, want %v", sample.HostTime, acceptedAt)
	}

	if len(sample.Random) != 64 {
		t.Errorf("random output = %q, want 32 bytes", sample.Random)
	}
}

func TestGuestClock_WarmedInstanceAndTemporaryInstance(t *testing.T) {
	initializedAt := time.Date(2040, 5, 6, 7, 8, 9, 123456000, time.UTC)
	var clockNS atomic.Int64
	clockNS.Store(initializedAt.UnixNano())
	rt, compiled := clockFixtureRuntime(t, func() time.Time { return time.Unix(0, clockNS.Load()) })
	pool := rt.NewPool("clockprobe", compiled, PoolConfig{WarmSize: 1, MaxSize: 1})
	t.Cleanup(func() { pool.DrainAndClose(context.Background(), time.Second) })

	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	for pool.IdleCount() == 0 {
		select {
		case <-deadline.C:
			t.Fatal("instance did not warm")
		case <-time.After(time.Millisecond):
		}
	}

	acceptedAt := initializedAt.Add(time.Hour)
	clockNS.Store(acceptedAt.UnixNano())
	inst, err := pool.Borrow(t.Context())
	if err != nil {
		t.Fatalf("borrow warmed instance: %v", err)
	}
	defer pool.Return(inst)
	rt.RegisterInstance(inst)
	defer rt.UnregisterInstance(inst)

	first := readGuestClockSample(t, WithInvocationTime(t.Context(), acceptedAt), rt, inst)
	assertGuestClockSample(t, first, initializedAt, acceptedAt)

	nextAcceptedAt := acceptedAt.Add(time.Minute)
	clockNS.Store(nextAcceptedAt.UnixNano())
	second := readGuestClockSample(t, t.Context(), rt, inst)
	assertGuestClockSample(t, second, initializedAt, nextAcceptedAt)

	temp, err := rt.InstantiateTemp(WithInvocationTime(t.Context(), acceptedAt), "clockprobe", compiled)
	if err != nil {
		t.Fatalf("instantiate temporary instance: %v", err)
	}
	t.Cleanup(func() { _ = temp.Module().Close(context.Background()) })
	rt.RegisterInstance(temp)
	defer rt.UnregisterInstance(temp)

	third := readGuestClockSample(t, WithInvocationTime(t.Context(), acceptedAt), rt, temp)
	assertGuestClockSample(t, third, nextAcceptedAt, acceptedAt)
	if first.Random == third.Random {
		t.Error("fresh instances returned identical random bytes")
	}
}

func TestGuestClock_EventJobAndCron(t *testing.T) {
	acceptedAt := time.Date(2040, 1, 2, 3, 4, 5, 0, time.UTC)
	rt, compiled := clockFixtureRuntime(t, func() time.Time { return acceptedAt })
	inst, err := rt.InstantiateTemp(t.Context(), "clockprobe", compiled)
	if err != nil {
		t.Fatalf("instantiate fixture: %v", err)
	}
	t.Cleanup(func() { _ = inst.Module().Close(context.Background()) })
	rt.RegisterInstance(inst)
	defer rt.UnregisterInstance(inst)

	for name, invoke := range map[string]func(context.Context, []byte) (int32, error){
		"event": inst.InvokeHandleEvent,
		"job":   inst.InvokeHandleJob,
		"cron":  inst.InvokeHandleCron,
	} {
		t.Run(name, func(t *testing.T) {
			payload, err := msgpack.Marshal(map[string]int64{"expected_ms": acceptedAt.UnixMilli(), "sleep_ms": 25})
			if err != nil {
				t.Fatalf("marshal invocation: %v", err)
			}

			status, err := invoke(t.Context(), payload)
			if err != nil || status != 0 {
				t.Fatalf("invoke %s: status = %d, error = %v", name, status, err)
			}
		})
	}
}
