package registry

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	abi "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/config"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/wasm"
	"github.com/djangbahevans/goerp/internal/engine/wasm/wasmtest"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

func TestHookRegistryOrder(t *testing.T) {
	reg := &ModuleRegistry{}
	snap, err := reg.Update(map[string]*module.LoadedModule{
		"z":      {Manifest: manifest.Manifest{Name: "z", Hooks: []manifest.Hook{{Hook: abi.HookRequestBefore, Handler: "z"}, {Hook: "custom.point", Handler: "custom"}}}},
		"a":      {Manifest: manifest.Manifest{Name: "a", Hooks: []manifest.Hook{{Hook: abi.HookRequestBefore, Handler: "zero", Priority: new(0)}, {Hook: abi.HookRequestBefore, Handler: "first"}, {Hook: abi.HookRequestBefore, Handler: "second"}, {Hook: abi.HookRequestBefore, Handler: "high", Priority: new(90)}}}},
		"failed": {Status: module.StatusFailed, Manifest: manifest.Manifest{Hooks: []manifest.Hook{{Hook: abi.HookRequestBefore, Handler: "bad", Priority: new(100)}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	entries := snap.Hooks(abi.HookRequestBefore)
	if len(entries) != 5 {
		t.Fatalf("entries = %+v", entries)
	}
	for i, want := range []string{"high", "first", "second", "z", "zero"} {
		if entries[i].Handler != want {
			t.Fatalf("entries = %+v", entries)
		}
	}
	if len(snap.Hooks("custom.point")) != 0 {
		t.Fatal("custom hooks must not execute")
	}
}

func TestLifecycleHooksRealDispatchAndUninstall(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hooks.wasm")
	cmd := exec.CommandContext(t.Context(), "go", "build", "-buildmode=c-shared", "-o", path, "../wasm/testdata/hookfixture")
	cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build fixture: %v\n%s", err, out)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	wasmtest.Precompile(t, data, wasmtest.SharedCompilationCacheDir())

	var output bytes.Buffer
	previousLogger := log.Logger
	log.Logger = zerolog.New(&output)
	rt, err := wasm.New(&config.Config{
		CompilationCache:  wasmtest.SharedCompilationCacheDir(),
		PoolMaxMemoryByes: 64 << 20,
		Environment:       string(config.Production),
	}, nil, nil, nil)
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
	observer := &module.LoadedModule{Manifest: manifest.Manifest{Name: "observer"}, Pool: pool, Status: module.StatusReady}
	for _, hook := range []string{abi.HookTenantProvisioned, abi.HookModuleInstalled, abi.HookModuleUpgraded, abi.HookModuleUninstalled} {
		observer.Manifest.Hooks = append(observer.Manifest.Hooks, manifest.Hook{Hook: hook, Handler: "reject", Priority: new(90)}, manifest.Hook{Hook: hook, Handler: "record"})
	}
	reg := &ModuleRegistry{}
	if _, err := reg.Update(map[string]*module.LoadedModule{"observer": observer, "subject": {Manifest: manifest.Manifest{Name: "subject"}, Status: module.StatusReady}}); err != nil {
		t.Fatal(err)
	}
	req := abi.Request{TenantID: "tenant", TenantSlug: "slug", UserID: "actor", TraceID: "trace"}
	snap := reg.Snapshot()
	snap.NotifyHooks(t.Context(), rt, HookInvocation{Hook: abi.HookTenantProvisioned, Request: req})
	snap.NotifyModuleChange(t.Context(), rt, req, "subject", "", "1.0.0")
	snap.NotifyModuleChange(t.Context(), rt, req, "subject", "1.0.0", "2.0.0")
	beforeNoop := output.Len()
	snap.NotifyModuleChange(t.Context(), rt, req, "subject", "2.0.0", "2.0.0")
	if output.Len() != beforeNoop {
		t.Fatal("same version emitted a lifecycle hook")
	}

	prepareError := errors.New("prepare failed")
	if _, err := reg.UninstallModule(t.Context(), rt, "subject", nil, func(context.Context, *module.LoadedModule) error { return prepareError }); !errors.Is(err, prepareError) {
		t.Fatalf("prepare error = %v", err)
	}
	if reg.Snapshot().Modules()["subject"] == nil {
		t.Fatal("failed uninstall removed module")
	}
	removed, err := reg.UninstallModule(t.Context(), rt, "subject", []HookInvocation{{Request: req}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if removed == nil || reg.Snapshot().Modules()["subject"] != nil {
		t.Fatal("module not removed")
	}
	for _, want := range []string{
		"HOOK:engine.tenant.provisioned::::tenant:slug:actor:trace",
		"HOOK:engine.module.installed:subject::1.0.0:tenant:slug:actor:trace",
		"HOOK:engine.module.upgraded:subject:1.0.0:2.0.0:tenant:slug:actor:trace",
		"HOOK:engine.module.uninstalled:subject:::tenant:slug:actor:trace",
	} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("missing %q in %s", want, output.String())
		}
	}
}

func TestUninstallModuleRejectsHardDependentsBeforeCleanup(t *testing.T) {
	reg := &ModuleRegistry{}
	if _, err := reg.Update(map[string]*module.LoadedModule{
		"subject":   {Manifest: manifest.Manifest{Name: "subject"}, Status: module.StatusReady},
		"dependent": {Manifest: manifest.Manifest{Name: "dependent", DependsOn: []string{"subject"}}, Status: module.StatusReady},
	}); err != nil {
		t.Fatal(err)
	}
	prepared := false
	_, err := reg.UninstallModule(t.Context(), nil, "subject", nil, func(context.Context, *module.LoadedModule) error {
		prepared = true
		return nil
	})
	if err == nil || prepared || reg.Snapshot().Modules()["subject"] == nil {
		t.Fatalf("error = %v, prepared = %v", err, prepared)
	}
}
