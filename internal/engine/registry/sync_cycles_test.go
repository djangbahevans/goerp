package registry

import (
	"strings"
	"testing"

	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/module"
)

func syncSub(name string) manifest.EventSubscription {
	return manifest.EventSubscription{Name: name, Handler: "handle_" + name, Async: false}
}

func asyncSub(name string) manifest.EventSubscription {
	return manifest.EventSubscription{Name: name, Handler: "handle_" + name, Async: true}
}

func TestValidateSyncSubscriptionCycles_SelfSubscriptionFails(t *testing.T) {
	modules := map[string]*module.LoadedModule{
		"billing": {
			Status: module.StatusSyncing,
			Manifest: manifest.Manifest{
				Emits:      []manifest.EventDeclaration{{Name: "invoice.created"}},
				Subscribes: []manifest.EventSubscription{syncSub("invoice.created")},
			},
		},
	}

	validateSyncSubscriptionCycles(modules)

	if modules["billing"].Status != module.StatusFailed {
		t.Fatalf("expected billing to be failed for self-subscription, got status %v", modules["billing"].Status)
	}
	if !strings.Contains(modules["billing"].FailureReason, "own emitted event") {
		t.Errorf("FailureReason = %q, want it to mention self-subscription", modules["billing"].FailureReason)
	}
}

func TestValidateSyncSubscriptionCycles_TwoModuleCycleFails(t *testing.T) {
	modules := map[string]*module.LoadedModule{
		"a": {
			Status: module.StatusSyncing,
			Manifest: manifest.Manifest{
				Emits:      []manifest.EventDeclaration{{Name: "a.done"}},
				Subscribes: []manifest.EventSubscription{syncSub("b.done")},
			},
		},
		"b": {
			Status: module.StatusSyncing,
			Manifest: manifest.Manifest{
				Emits:      []manifest.EventDeclaration{{Name: "b.done"}},
				Subscribes: []manifest.EventSubscription{syncSub("a.done")},
			},
		},
	}

	validateSyncSubscriptionCycles(modules)

	if modules["a"].Status != module.StatusFailed {
		t.Errorf("expected module a to be failed, got status %v", modules["a"].Status)
	}
	if modules["b"].Status != module.StatusFailed {
		t.Errorf("expected module b to be failed, got status %v", modules["b"].Status)
	}
}

func TestValidateSyncSubscriptionCycles_ThreeModuleCycleFails(t *testing.T) {
	modules := map[string]*module.LoadedModule{
		"a": {
			Status: module.StatusSyncing,
			Manifest: manifest.Manifest{
				Emits:      []manifest.EventDeclaration{{Name: "a.done"}},
				Subscribes: []manifest.EventSubscription{syncSub("c.done")},
			},
		},
		"b": {
			Status: module.StatusSyncing,
			Manifest: manifest.Manifest{
				Emits:      []manifest.EventDeclaration{{Name: "b.done"}},
				Subscribes: []manifest.EventSubscription{syncSub("a.done")},
			},
		},
		"c": {
			Status: module.StatusSyncing,
			Manifest: manifest.Manifest{
				Emits:      []manifest.EventDeclaration{{Name: "c.done"}},
				Subscribes: []manifest.EventSubscription{syncSub("b.done")},
			},
		},
	}

	validateSyncSubscriptionCycles(modules)

	for _, name := range []string{"a", "b", "c"} {
		if modules[name].Status != module.StatusFailed {
			t.Errorf("expected module %q to be failed, got status %v", name, modules[name].Status)
		}
	}
}

func TestValidateSyncSubscriptionCycles_AsyncEdgesDoNotCount(t *testing.T) {
	// Same shape as the two-module cycle above, but both subscriptions are
	// async — async dispatch can never deadlock, so this must be accepted.
	modules := map[string]*module.LoadedModule{
		"a": {
			Status: module.StatusSyncing,
			Manifest: manifest.Manifest{
				Emits:      []manifest.EventDeclaration{{Name: "a.done"}},
				Subscribes: []manifest.EventSubscription{asyncSub("b.done")},
			},
		},
		"b": {
			Status: module.StatusSyncing,
			Manifest: manifest.Manifest{
				Emits:      []manifest.EventDeclaration{{Name: "b.done"}},
				Subscribes: []manifest.EventSubscription{asyncSub("a.done")},
			},
		},
	}

	validateSyncSubscriptionCycles(modules)

	if modules["a"].Status == module.StatusFailed || modules["b"].Status == module.StatusFailed {
		t.Fatalf("async-only cycle must not be rejected: a=%v b=%v", modules["a"].Status, modules["b"].Status)
	}
}

func TestValidateSyncSubscriptionCycles_MixedGraphOnlySyncEdgeCycles(t *testing.T) {
	// a -> b is sync (cycle-forming with b -> a sync below); b -> c is
	// async and c emits nothing back to b, so only the a/b sync pair
	// should fail.
	modules := map[string]*module.LoadedModule{
		"a": {
			Status: module.StatusSyncing,
			Manifest: manifest.Manifest{
				Emits:      []manifest.EventDeclaration{{Name: "a.done"}},
				Subscribes: []manifest.EventSubscription{syncSub("b.done")},
			},
		},
		"b": {
			Status: module.StatusSyncing,
			Manifest: manifest.Manifest{
				Emits: []manifest.EventDeclaration{{Name: "b.done"}},
				Subscribes: []manifest.EventSubscription{
					syncSub("a.done"),
					asyncSub("c.done"),
				},
			},
		},
		"c": {
			Status:   module.StatusSyncing,
			Manifest: manifest.Manifest{Emits: []manifest.EventDeclaration{{Name: "c.done"}}},
		},
	}

	validateSyncSubscriptionCycles(modules)

	if modules["a"].Status != module.StatusFailed || modules["b"].Status != module.StatusFailed {
		t.Fatalf("expected the sync a/b cycle to fail: a=%v b=%v", modules["a"].Status, modules["b"].Status)
	}
	if modules["c"].Status == module.StatusFailed {
		t.Fatalf("module c has no part in the cycle and must not be failed")
	}
}

func TestValidateSyncSubscriptionCycles_NonCyclicAccepted(t *testing.T) {
	modules := map[string]*module.LoadedModule{
		"a": {
			Status:   module.StatusSyncing,
			Manifest: manifest.Manifest{Emits: []manifest.EventDeclaration{{Name: "a.done"}}},
		},
		"b": {
			Status: module.StatusSyncing,
			Manifest: manifest.Manifest{
				Emits:      []manifest.EventDeclaration{{Name: "b.done"}},
				Subscribes: []manifest.EventSubscription{syncSub("a.done")},
			},
		},
	}

	validateSyncSubscriptionCycles(modules)

	if modules["a"].Status == module.StatusFailed || modules["b"].Status == module.StatusFailed {
		t.Fatalf("non-cyclic sync subscription graph must not be rejected: a=%v b=%v", modules["a"].Status, modules["b"].Status)
	}
}
