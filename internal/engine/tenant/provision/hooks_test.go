package tenantprovision

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	abi "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/config"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
	"github.com/djangbahevans/goerp/internal/engine/user"
	"github.com/djangbahevans/goerp/internal/engine/wasm"
	"github.com/djangbahevans/goerp/internal/engine/wasm/wasmtest"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

type hookOutput struct {
	mu   sync.Mutex
	data bytes.Buffer
}

func (o *hookOutput) Write(data []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.data.Write(data)
}

func (o *hookOutput) String() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.data.String()
}

func TestProvisionLifecycleHooksRealWorkflow(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hooks.wasm")
	cmd := exec.CommandContext(t.Context(), "go", "build", "-buildmode=c-shared", "-o", path, "../../wasm/testdata/hookfixture")
	cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build fixture: %v\n%s", err, out)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	output := &hookOutput{}
	previousLogger := log.Logger
	log.Logger = zerolog.New(output)
	rt, err := wasm.New(&config.Config{CompilationCache: wasmtest.SharedCompilationCacheDir(), PoolMaxMemoryByes: 64 << 20, Environment: string(config.Production)}, nil, nil, nil)
	log.Logger = previousLogger
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rt.Close(context.Background()) })
	compiled, err := rt.CompileModule(t.Context(), data)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = compiled.Close(context.Background()) })
	pool := rt.NewPool("observer", compiled, wasm.PoolConfig{MaxSize: 1, WarmSize: 1})
	t.Cleanup(func() { pool.DrainAndClose(context.Background(), 5*time.Second) })
	mod := &module.LoadedModule{Manifest: manifest.Manifest{Name: "observer", Version: "1.0.0", Hooks: []manifest.Hook{
		{Hook: abi.HookTenantProvisioned, Handler: "record"}, {Hook: abi.HookModuleInstalled, Handler: "record"},
	}}, Status: module.StatusReady, Pool: pool}
	env := newTestEnv(t, map[string]*module.LoadedModule{"observer": mod})
	env.activities.Runtime = rt
	slug := uniqueSlug(t)

	userID, err := user.NewStore(env.conn).CreateRegistered(t.Context(), slug+"@example.com", "fixture-hash", user.StatusActive)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = tenantschema.Drop(context.Background(), env.conn, slug)
		_, _ = env.conn.Exec("DELETE FROM system.tenants WHERE slug = $1", slug)
		_, _ = env.conn.Exec("DELETE FROM system.users WHERE id = $1", userID)
	})

	if err := env.runWorkflow(t, Input{
		Slug:           slug,
		Name:           "Hook tenant",
		ExistingUserID: userID,
		RequestID:      "request",
		TraceID:        "trace",
	}); err != nil {
		t.Fatal(err)
	}
	tenant, err := env.tenantStore.GetBySlug(t.Context(), slug)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"HOOK:engine.module.installed:observer::1.0.0:" + tenant.ID + ":" + slug + ":" + userID + ":trace",
		"HOOK:engine.tenant.provisioned::::" + tenant.ID + ":" + slug + ":" + userID + ":trace",
	} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("missing %q in %s", want, output.String())
		}
	}
	before := output.String()
	if err := env.activities.SyncModuleSchema(t.Context(), tenant.ID, slug, "observer", abi.Request{TraceID: "trace"}); err != nil {
		t.Fatal(err)
	}
	if output.String() != before {
		t.Fatal("schema retry repeated install notification")
	}
}
