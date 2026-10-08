package loader

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/config"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/wasm"
	"github.com/djangbahevans/goerp/internal/engine/wasm/wasmtest"
)

func TestLoadModule_TransactionalSubscriptions(t *testing.T) {
	cases := []struct {
		name          string
		async         bool
		transactional bool
		policy        map[string]any
		wantError     string
	}{
		{name: "default retries", async: true, transactional: true},
		{name: "synchronous", transactional: true, wantError: "requires async: true"},
		{
			name: "retention boundary", async: true, transactional: true,
			policy: map[string]any{"max_attempts": 2, "backoff": "linear", "initial_delay_ms": 1_200_000},
		},
		{
			name: "retention exceeded", async: true, transactional: true,
			policy:    map[string]any{"max_attempts": 2, "backoff": "linear", "initial_delay_ms": 1_200_001},
			wantError: "GOERP_EVENT_LEDGER_RETENTION",
		},
		{
			name: "capped exponential", async: true, transactional: true,
			policy: map[string]any{"max_attempts": 25, "backoff": "exponential", "initial_delay_ms": 120_000, "max_delay_ms": 120_000, "jitter": false},
		},
		{
			name: "jitter cannot shorten worst case", async: true, transactional: true,
			policy:    map[string]any{"max_attempts": 2, "backoff": "none", "initial_delay_ms": 1_800_001, "jitter": true},
			wantError: "GOERP_EVENT_LEDGER_RETENTION",
		},
		{
			name: "plain subscription", async: true,
			policy: map[string]any{"max_attempts": 2, "backoff": "none", "initial_delay_ms": 3_600_000},
		},
		{
			name: "duration overflow", async: true, transactional: true,
			policy:    map[string]any{"max_attempts": 2, "backoff": "exponential", "initial_delay_ms": int64(1<<63 - 1)},
			wantError: "time.Duration",
		},
		{
			name: "negative cap", async: true, transactional: true,
			policy:    map[string]any{"max_attempts": 2, "backoff": "linear", "initial_delay_ms": 100, "max_delay_ms": -1},
			wantError: "time.Duration",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rt, err := wasm.New(&config.Config{
				CompilationCache:     wasmtest.SharedCompilationCacheDir(),
				PoolMaxMemoryByes:    1 << 20,
				Environment:          string(config.Production),
				EventLedgerRetention: time.Hour,
			}, nil, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			cleanupCtx := context.WithoutCancel(t.Context())
			t.Cleanup(func() { _ = rt.Close(cleanupCtx) })

			sub := map[string]any{"name": "demo.order.created", "async": tc.async, "transactional": tc.transactional}
			if tc.policy != nil {
				sub["retry_policy"] = tc.policy
			}
			src := Source{
				Name:      "demo",
				WasmBytes: okModule,
				ManifestBytes: manifestJSONWithFields(t, "demo", okModule, []string{"db.read", "db.write"}, map[string]any{
					"subscribes": []map[string]any{sub},
				}),
			}

			loaded := LoadModule(t.Context(), rt, wasm.PoolConfig{MaxSize: 1}, src)
			if loaded.CompiledModule != nil {
				t.Cleanup(func() { _ = loaded.CompiledModule.Close(cleanupCtx) })
			}
			if loaded.Pool != nil {
				t.Cleanup(func() { loaded.Pool.DrainAndClose(cleanupCtx, time.Second) })
			}

			if tc.wantError != "" {
				if loaded.Status != module.StatusFailed || !strings.Contains(loaded.FailureReason, tc.wantError) {
					t.Fatalf("status=%s reason=%q, want failure containing %q", loaded.Status, loaded.FailureReason, tc.wantError)
				}
				return
			}

			if loaded.Status != module.StatusSyncing {
				t.Fatalf("status=%s reason=%q, want syncing", loaded.Status, loaded.FailureReason)
			}
		})
	}
}
