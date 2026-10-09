package engine

import (
	"context"
	"encoding/json/v2"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/auth/membership/membershiptest"
	"github.com/djangbahevans/goerp/internal/engine/config"
	"github.com/djangbahevans/goerp/internal/engine/httpx"
	"github.com/djangbahevans/goerp/internal/engine/wasm/wasmtest"
	"google.golang.org/grpc"
)

func baseTestConfig(t *testing.T) *config.Config {
	t.Helper()
	conn := membershiptest.New(t)
	var database string
	if err := conn.QueryRowContext(t.Context(), `SELECT current_database()`).Scan(&database); err != nil {
		t.Fatal(err)
	}

	t.Setenv("GOERP_STORAGE_LOCAL_DIR", t.TempDir())
	t.Setenv("GOERP_ADMIN_TOKEN", "test-admin-token")
	// 127.0.0.1, not "localhost": gRPC's dialer can stall for several
	// seconds trying an unreachable ::1 first in IPv6-loopback-but-
	// unrouted environments (see internal/engine/temporal's own tests).
	t.Setenv("GOERP_TEMPORAL_HOST_PORT", "127.0.0.1:7233")

	return &config.Config{
		ListenAddr:               ":0",
		AppBaseURL:               "http://localhost:8080",
		PlatformDomain:           "localhost",
		AdminAddr:                "127.0.0.1:0",
		Environment:              "development",
		LogLevel:                 "info",
		LogFormat:                "text",
		ShutdownTimeout:          time.Second,
		ShutdownDrainDelay:       0,
		DBPrimaryDSN:             "postgres://engine_user:dev@localhost:15432/" + database,
		DBSchemaSyncDSN:          "postgres://schema_sync_user:dev@localhost:15432/" + database,
		RedisAddr:                "localhost:6379",
		RedisMaxRetries:          1,
		SecretsBackend:           "env",
		StorageBackend:           "local",
		CompilationCache:         wasmtest.SharedCompilationCacheDir(),
		PoolMaxMemoryByes:        16 * 1024 * 1024,
		OTelExporterOTLPEndpoint: "",
		OTelServiceName:          "goerp-engine",
		OTelInsecure:             true,
	}
}

func closeTestEnginePools(t *testing.T, e *Engine) {
	t.Helper()
	t.Cleanup(func() {
		e.jobQueuePool.Close()
		_ = e.syncPool.Raw().Close()
		_ = e.primaryDB.Close()
		if e.replicaDB != nil {
			_ = e.replicaDB.Close()
		}
	})
}

func requireEngineConstruction(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("initialize private engine test database: %v", err)
	}
}

func TestNewSuccess(t *testing.T) {
	cfg := baseTestConfig(t)

	e, err := New(cfg)
	requireEngineConstruction(t, err)
	closeTestEnginePools(t, e)

	if e.primaryDB == nil {
		t.Error("primaryDB is nil after a successful New()")
	}
	if e.replicaDB != nil {
		t.Error("replicaDB is non-nil when DBReplicaDSN was never set")
	}
	if e.secretsBackend == nil {
		t.Error("secretsBackend is nil after a successful New()")
	}
	if e.Tracer() == nil {
		t.Error("Tracer() is nil after a successful New()")
	}
}

func TestModuleDevStartsWithoutTemporal(t *testing.T) {
	cfg := baseTestConfig(t)
	cfg.ModuleDev = true
	cfg.ModuleDir = t.TempDir()
	t.Setenv("GOERP_TEMPORAL_HOST_PORT", "127.0.0.1:1")

	e, err := New(cfg)
	requireEngineConstruction(t, err)
	closeTestEnginePools(t, e)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := e.Shutdown(ctx); err != nil {
			t.Errorf("shutdown module-dev engine: %v", err)
		}
	})

	if e.temporalClient != nil {
		t.Fatal("module-dev engine connected to Temporal")
	}
	if err := e.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !e.readiness.Load() {
		t.Fatal("module-dev engine did not become ready")
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
	requirePrimaryConnection(t, cfg, err)
	if err == nil {
		t.Fatal("New() with unreachable Redis: expected an error (fail-hard per engine-internals.md §2 step 5, not warn-only), got nil")
	}
}

func TestNewReplicaDBUnreachableWarnsOnly(t *testing.T) {
	cfg := baseTestConfig(t)
	cfg.DBReplicaDSN = "postgres://user:pass@127.0.0.1:1/db"

	e, err := New(cfg)
	requireEngineConstruction(t, err)
	closeTestEnginePools(t, e)

	if err != nil {
		t.Fatalf("New() with an unreachable replica DB: expected success (warn-only per engine-internals.md §2 step 4), got error: %v", err)
	}
	if e.replicaDB != nil {
		t.Error("replicaDB should be nil when the replica connection failed")
	}
}

func requirePrimaryConnection(t *testing.T, cfg *config.Config, gotErr error) {
	t.Helper()
	if gotErr == nil {
		return
	}
	control := *cfg
	control.RedisAddr = "localhost:6379"
	e, controlErr := New(&control)
	if controlErr != nil {
		t.Fatalf("initialize private engine test database: %v", controlErr)
	}

	closeTestEnginePools(t, e)
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

func TestStart_FailsWhenAListenerCannotBind(t *testing.T) {
	for _, tc := range []struct {
		name string
		set  func(cfg *config.Config, addr string)
	}{
		{"http", func(cfg *config.Config, addr string) { cfg.ListenAddr = addr }},
		{"admin", func(cfg *config.Config, addr string) { cfg.AdminAddr = addr }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			held, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatalf("hold a test port: %v", err)
			}
			t.Cleanup(func() { _ = held.Close() })
			addr := held.Addr().String()

			cfg := baseTestConfig(t)
			tc.set(cfg, addr)

			e, newErr := New(cfg)
			requireEngineConstruction(t, newErr)
			closeTestEnginePools(t, e)

			err = e.Start(t.Context())
			if err == nil {
				_ = e.Shutdown(t.Context())
				t.Fatalf("Start() = nil, want an error for the held address %s", addr)
			}
			if !strings.Contains(err.Error(), addr) {
				t.Errorf("Start() error = %q, want it to name %s", err, addr)
			}
			if e.readiness.Load() {
				t.Error("readiness = true after a failed Start, want false")
			}
		})
	}
}

// Default configuration leaves optional dependency clients nil; the health endpoint must
// tolerate their absence.
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
	requireEngineConstruction(t, newErr)
	closeTestEnginePools(t, e)

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
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
	if err := json.UnmarshalRead(resp.Body, &report); err != nil {
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

func TestNewWithOTelEndpoint(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve a test port: %v", err)
	}
	srv := grpc.NewServer()
	go func() { _ = srv.Serve(ln) }()
	defer srv.Stop()

	cfg := baseTestConfig(t)
	cfg.OTelExporterOTLPEndpoint = ln.Addr().String()

	e, newErr := New(cfg)
	requireEngineConstruction(t, newErr)
	closeTestEnginePools(t, e)

	if e.Tracer() == nil {
		t.Fatal("expected non-nil Tracer(), got nil")
	}
	if e.tracerProvider == nil {
		t.Fatal("expected non-nil tracerProvider, got nil")
	}

	_, span := e.Tracer().Start(t.Context(), "engine.test.span")
	span.End()

	shutdownCtx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	if err := e.Shutdown(shutdownCtx); err != nil {
		t.Errorf("Shutdown() error: %v", err)
	}
}

// Use a malformed target to force synchronous setup failure; unreachable but valid hosts
// are dialed lazily and do not test this branch.
func TestNewMalformedOTelEndpointWarnsOnly(t *testing.T) {
	cfg := baseTestConfig(t)
	cfg.OTelExporterOTLPEndpoint = "not a valid endpoint!!! \x00"

	e, err := New(cfg)
	requireEngineConstruction(t, err)
	closeTestEnginePools(t, e)

	if err != nil {
		t.Fatalf("New() with a malformed OTel endpoint: expected success (warn-only per engine-internals.md §2), got error: %v", err)
	}
	if e.Tracer() == nil {
		t.Fatal("expected non-nil Tracer() fallback, got nil")
	}
}
