package wasm

import (
	"context"
	"encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/djangbahevans/goerp/internal/engine/config"
	"github.com/djangbahevans/goerp/internal/engine/wasm/wasmtest"
	"github.com/djangbahevans/goerp/internal/engine/ws"
	"github.com/vmihailenco/msgpack/v5"
)

func TestUIPushFromJobHandler(t *testing.T) {
	ctx := t.Context()
	wasmPath := filepath.Join(t.TempDir(), "uipush.wasm")
	cmd := exec.CommandContext(ctx, "go", "build", "-buildmode=c-shared", "-o", wasmPath, "./testdata/uipushfixture")
	cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build UI push fixture: %v\n%s", err, out)
	}
	data, err := os.ReadFile(wasmPath)
	if err != nil {
		t.Fatal(err)
	}

	wasmtest.Precompile(t, data, wasmtest.SharedCompilationCacheDir())

	rt, err := New(&config.Config{
		CompilationCache:  wasmtest.SharedCompilationCacheDir(),
		PoolMaxMemoryByes: 64 << 20,
		Environment:       string(config.Production),
	}, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	cleanupCtx := context.WithoutCancel(ctx)
	t.Cleanup(func() { _ = rt.Close(cleanupCtx) })
	hub := ws.NewHub()
	rt.SetUIPusher(hub)
	t.Cleanup(func() { hub.Close(cleanupCtx) })

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.CloseNow() }()
		_ = hub.Serve(r.Context(), conn, "conn1", "u1", "tenant1", "test", ws.AccessChecks{})
	}))
	t.Cleanup(server.Close)
	conn, _, err := websocket.Dial(ctx, "ws://"+server.Listener.Addr().String(), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.CloseNow() })
	channel := "ui:user:u1"
	if err := wsjson.Write(ctx, conn, map[string]string{"type": "subscribe", "channel": channel}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		count, err := hub.BroadcastCounted(ctx, channel, "tenant1", "barrier", nil)
		if err != nil {
			t.Fatal(err)
		}
		if count == 1 {
			var barrier map[string]any
			if err := wsjson.Read(ctx, conn, &barrier); err != nil {
				t.Fatal(err)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("UI subscription did not register")
		}
		time.Sleep(time.Millisecond)
	}

	compiled, err := rt.CompileModule(ctx, data)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = compiled.Close(cleanupCtx) })
	inst, err := newModuleInstance(ctx, "uipushfixture", compiled, rt)
	if err != nil {
		t.Fatal(err)
	}
	mc := NewModuleContext("job1", "uipushfixture", "u1", "", nil, nil, "tenant1", "acme", "", abi.CapUIPush, rt.TxLimiter(), ModuleSnapshot{})
	inst.SetModuleContext(mc)
	rt.RegisterInstance(inst)
	t.Cleanup(func() { rt.UnregisterInstance(inst) })
	envelope, err := msgpack.Marshal(abiv1.JobEnvelope{
		JobID:      "job1",
		JobType:    "confirm",
		TenantID:   "tenant1",
		UserID:     "u1",
		ModuleName: "uipushfixture",
	})
	if err != nil {
		t.Fatal(err)
	}
	status, err := inst.InvokeHandleJob(ctx, envelope)
	if err != nil || status != 0 {
		t.Fatalf("job status = %d, %v", status, err)
	}

	readCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_, raw, err := conn.Read(readCtx)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"channel":"ui:user:u1","type":"ui.push","payload":{"instructions":[
		{"type":"toast","message":"Payment received","severity":"success","duration_ms":0},
		{"type":"reload","resource":"sales.invoice","record_id":"inv1"},
		{"type":"set_badge","target":"/sales/count","count":0}
	]}}`
	var gotValue, wantValue any
	if err := json.Unmarshal(raw, &gotValue); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(want), &wantValue); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(gotValue, wantValue) {
		t.Fatalf("job push = %s, want %s", raw, want)
	}
}
