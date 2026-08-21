package engine

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/config"
	"github.com/djangbahevans/goerp/internal/engine/httpx"
)

// baseTestConfig returns a Config pointing at the compose.dev.yml dev stack
// (README.md's local development section: PgBouncer on 6432, direct
// Postgres on 55432 for schema sync, Redis on 6379, credentials
// goerp/dev/goerp). Individual tests override the one field they're
// exercising.
func baseTestConfig(t *testing.T) *config.Config {
	t.Helper()
	t.Setenv("GOERP_STORAGE_LOCAL_DIR", t.TempDir())
	t.Setenv("GOERP_ADMIN_TOKEN", "test-admin-token")
	// 127.0.0.1, not "localhost": gRPC's dialer can stall for several
	// seconds trying an unreachable ::1 first in IPv6-loopback-but-
	// unrouted environments (see internal/engine/temporal's own tests).
	t.Setenv("GOERP_TEMPORAL_HOST_PORT", "127.0.0.1:7233")

	return &config.Config{
		ListenAddr:         ":0",
		AdminAddr:          "127.0.0.1:0",
		Environment:        "development",
		LogLevel:           "info",
		LogFormat:          "text",
		ShutdownTimeout:    time.Second,
		ShutdownDrainDelay: 0,
		DBPrimaryDSN:       "postgres://goerp:dev@localhost:6432/goerp",
		DBSchemaSyncDSN:    "postgres://goerp:dev@localhost:55432/goerp",
		RedisAddr:          "localhost:6379",
		RedisMaxRetries:    1,
		SecretsBackend:     "env",
		StorageBackend:     "local",
		CompilationCache:   filepath.Join(t.TempDir(), "wasm-cache"),
		PoolMaxMemoryByes:  16 * 1024 * 1024,
	}
}

func skipIfInfraUnreachable(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Skipf("dev infra not reachable (start compose.dev.yml): %v", err)
	}
}

func TestNewSuccess(t *testing.T) {
	cfg := baseTestConfig(t)

	e, err := New(cfg)
	skipIfInfraUnreachable(t, err)
	t.Cleanup(func() { _ = e.primaryDB.Close() })

	if e.primaryDB == nil {
		t.Error("primaryDB is nil after a successful New()")
	}
	if e.replicaDB != nil {
		t.Error("replicaDB is non-nil when DBReplicaDSN was never set")
	}
	if e.secretsBackend == nil {
		t.Error("secretsBackend is nil after a successful New()")
	}
}

func TestNewPrimaryDBUnreachableFailsHard(t *testing.T) {
	cfg := baseTestConfig(t)
	cfg.DBPrimaryDSN = "postgres://user:pass@127.0.0.1:1/db"

	_, err := New(cfg)
	if err == nil {
		t.Fatal("New() with an unreachable primary DB: expected an error (fail-hard per engine-internals.md §2 step 3), got nil")
	}
}

func TestNewRedisUnreachableFailsHard(t *testing.T) {
	cfg := baseTestConfig(t)
	cfg.RedisAddr = "127.0.0.1:1"

	_, err := New(cfg)
	skipIfPrimaryUnreachable(t, cfg, err)
	if err == nil {
		t.Fatal("New() with unreachable Redis: expected an error (fail-hard per engine-internals.md §2 step 5, not warn-only), got nil")
	}
}

func TestNewReplicaDBUnreachableWarnsOnly(t *testing.T) {
	cfg := baseTestConfig(t)
	cfg.DBReplicaDSN = "postgres://user:pass@127.0.0.1:1/db"

	e, err := New(cfg)
	skipIfInfraUnreachable(t, err)
	t.Cleanup(func() { _ = e.primaryDB.Close() })

	if err != nil {
		t.Fatalf("New() with an unreachable replica DB: expected success (warn-only per engine-internals.md §2 step 4), got error: %v", err)
	}
	if e.replicaDB != nil {
		t.Error("replicaDB should be nil when the replica connection failed")
	}
}

// skipIfPrimaryUnreachable distinguishes "the test's own primary DB isn't
// reachable" (skip, same as skipIfInfraUnreachable) from "Redis was
// unreachable as intended by the test" (not a skip condition) by checking
// whether a healthy control run against baseTestConfig succeeds first.
func skipIfPrimaryUnreachable(t *testing.T, cfg *config.Config, gotErr error) {
	t.Helper()
	if gotErr == nil {
		return
	}
	control := *cfg
	control.RedisAddr = "localhost:6379"
	if _, controlErr := New(&control); controlErr != nil {
		t.Skipf("dev infra not reachable (start compose.dev.yml): %v", controlErr)
	}
}

func TestNewUnknownSecretsBackendFailsHard(t *testing.T) {
	cfg := baseTestConfig(t)
	cfg.SecretsBackend = "nonsense"

	_, err := New(cfg)
	if err == nil {
		t.Fatal("New() with an unknown secrets backend: expected an error, got nil")
	}
}

func TestNewEmptyAdminTokenFailsHard(t *testing.T) {
	cfg := baseTestConfig(t)
	t.Setenv("GOERP_ADMIN_TOKEN", "")

	_, err := New(cfg)
	if err == nil {
		t.Fatal("New() with an empty GOERP_ADMIN_TOKEN: expected an error, got nil")
	}
}

// TestHealthEndpointDefaultConfigDoesNotPanic hits the real /_health
// endpoint over HTTP with the default config: no replica, no Meilisearch
// configured, both leaving their respective clients nil. This is a
// regression test for a real panic — the health closure used to call
// replicaPool.Ping/searchClient.Ping/storageBackend.Exists unconditionally,
// which segfaults on a nil *sql.DB/*search.Client/nil interface, and
// nil is exactly what those are in this — the default, not edge-case —
// configuration.
func TestHealthEndpointDefaultConfigDoesNotPanic(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve a test port: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	cfg := baseTestConfig(t)
	cfg.ListenAddr = addr

	e, newErr := New(cfg)
	skipIfInfraUnreachable(t, newErr)
	t.Cleanup(func() { _ = e.primaryDB.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := e.Start(ctx); err != nil {
		t.Fatalf("Start() error: %v", err)
	}
	t.Cleanup(func() {
		if err := e.Shutdown(context.Background()); err != nil {
			t.Errorf("Shutdown() error: %v", err)
		}
	})

	var resp *http.Response
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		resp, err = http.Get("http://" + addr + "/_health")
		if err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("GET /_health never succeeded (server may have panicked): %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	var report httpx.HealthReport
	if err := json.NewDecoder(resp.Body).Decode(&report); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if report.Status != "healthy" {
		t.Errorf("Status = %q, want %q (all checks should read ok/unconfigured, not error): %+v", report.Status, "healthy", report.Checks)
	}
	for _, name := range []string{"postgres_primary", "postgres_replica", "redis", "meilisearch", "object_storage"} {
		if _, ok := report.Checks[name]; !ok {
			t.Errorf("Checks missing %q", name)
		}
	}
}
