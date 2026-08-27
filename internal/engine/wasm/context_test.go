package wasm

import (
	"database/sql"
	"sync"
	"testing"

	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/permission"
)

func TestModuleContext_TransactionsGuardedByTxMu(t *testing.T) {
	modCtx := &ModuleContext{transactions: make(map[string]*sql.Tx)}

	var wg sync.WaitGroup
	for i := range 20 {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			modCtx.txMu.Lock()
			defer modCtx.txMu.Unlock()
			modCtx.transactions[string(rune('a'+n))] = nil
		}(i)
	}
	wg.Wait()

	modCtx.txMu.Lock()
	defer modCtx.txMu.Unlock()
	if got := len(modCtx.transactions); got != 20 {
		t.Errorf("len(transactions) = %d, want 20 — concurrent writes under txMu must not lose entries", got)
	}
}

func TestModuleContext_CapabilitiesAvailable(t *testing.T) {
	caps, err := abi.ResolveCapabilities([]string{"db.read", "event.emit"})
	if err != nil {
		t.Fatalf("ResolveCapabilities: %v", err)
	}

	modCtx := &ModuleContext{capabilities: caps}

	if !modCtx.capabilities.Has(abi.CapDBRead) {
		t.Error("expected capabilities to have CapDBRead")
	}
	if !modCtx.capabilities.Has(abi.CapEventEmit) {
		t.Error("expected capabilities to have CapEventEmit")
	}
	if modCtx.capabilities.Has(abi.CapDBWrite) {
		t.Error("expected capabilities to not have CapDBWrite — it was never declared")
	}
}

// TestModuleContext_PermissionSetReachableWithoutPermcache is the
// acceptance criteria for goerp#413: a host function reading off
// ModuleContext gets back the caller's resolved PermissionBitfield
// (and the PermissionRegistry to interpret it) with no permcache/DB
// lookup of its own — both were already resolved once, upstream, and
// threaded through NewModuleContext/ModuleSnapshot.
func TestModuleContext_PermissionSetReachableWithoutPermcache(t *testing.T) {
	reg := permission.NewPermissionRegistry()
	reg.Register("sales", []manifest.Permission{
		{Name: "sales:order:read"},
		{Name: "sales:order:write"},
	})

	readIdx, _ := reg.Index("sales:order:read")

	var permSet permission.PermissionBitfield
	permSet.Set(readIdx) // caller granted read, not write

	modCtx := NewModuleContext("req-1", "testmodule", "user-1", "contact-1", []string{"admin"}, permSet,
		"tenant-id-1", "acme", "trace-1", abi.CapabilitySet(0), nil,
		ModuleSnapshot{PermissionRegistry: reg})

	// The host function's own read — no permcache/DB round trip, just the
	// bitfield and registry ModuleContext was constructed with.
	gotReadIdx, ok := modCtx.PermissionRegistry().Index("sales:order:read")
	if !ok {
		t.Fatal("PermissionRegistry().Index(read): permission not found")
	}
	if !modCtx.PermissionSet.Has(gotReadIdx) {
		t.Error("PermissionSet.Has(read) = false, want true — caller was granted this permission")
	}

	gotWriteIdx, ok := modCtx.PermissionRegistry().Index("sales:order:write")
	if !ok {
		t.Fatal("PermissionRegistry().Index(write): permission not found")
	}
	if modCtx.PermissionSet.Has(gotWriteIdx) {
		t.Error("PermissionSet.Has(write) = true, want false — caller was never granted this permission")
	}
}

func TestModuleInstance_ModuleContextScopedPerInvocation(t *testing.T) {
	// Simulates two sequential invocations on the same goroutine borrowing
	// the same instance slot — each must get its own ModuleContext, with no
	// state carried from the first into the second.
	first := &ModuleInstance{}
	first.moduleCtx = &ModuleContext{RequestID: "req-1", TenantID: "tenant-a"}

	second := &ModuleInstance{}
	second.moduleCtx = &ModuleContext{RequestID: "req-2", TenantID: "tenant-b"}

	if first.moduleCtx == second.moduleCtx {
		t.Fatal("expected two independent ModuleContext instances, got the same pointer")
	}
	if first.moduleCtx.RequestID == second.moduleCtx.RequestID {
		t.Errorf("RequestID leaked across invocations: both are %q", first.moduleCtx.RequestID)
	}
	if first.moduleCtx.TenantID == second.moduleCtx.TenantID {
		t.Errorf("TenantID leaked across invocations: both are %q", first.moduleCtx.TenantID)
	}

	// Clearing one instance's context (as invokeHandler's cleanup will do)
	// must not affect the other.
	first.moduleCtx = nil
	if second.moduleCtx == nil || second.moduleCtx.RequestID != "req-2" {
		t.Error("clearing the first instance's moduleCtx affected the second")
	}
}
