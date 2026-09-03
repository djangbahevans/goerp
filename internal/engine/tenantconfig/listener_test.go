package tenantconfig

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/registry"
)

// startListener starts a Listener in the background and blocks until its
// LISTEN has actually been issued, so the caller's next Set() is
// guaranteed to be seen. Returns a func that stops the listener, waits
// for its goroutine to exit, and fails the test if Run returned a
// non-nil error for any reason other than the stop it was just asked for.
func startListener(t *testing.T, l *Listener) func() {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan struct{})
	runErr := make(chan error, 1)
	go func() {
		runErr <- l.Run(ctx, func() { close(ready) })
	}()

	select {
	case <-ready:
	case err := <-runErr:
		t.Fatalf("listener exited before becoming ready: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("listener did not become ready (LISTEN not issued) in time")
	}

	return func() {
		cancel()
		select {
		case err := <-runErr:
			if err != nil {
				t.Errorf("Run() = %v, want nil after a requested stop", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("listener did not stop in time")
		}
	}
}

func TestListener_InvalidatesOtherInstancesCacheOnConfigWrite(t *testing.T) {
	env := openTestEnv(t)
	tt := env.createTenant(t)
	env.createModuleConfigSchema(t, tt.Slug)

	if err := env.store.Set(context.Background(), tt.ID, "contacts.default_country_code", "DE"); err != nil {
		t.Fatalf("initial Set() error: %v", err)
	}

	// Two independent Resolver instances, each with its own private
	// cache — standing in for two separate engine processes sharing one
	// Postgres.
	resolverA := NewResolver(env.store, env.tenantStore, &registry.ModuleRegistry{})
	resolverB := NewResolver(env.store, env.tenantStore, &registry.ModuleRegistry{})

	stopA := startListener(t, NewListener(env.conn, resolverA))
	defer stopA()
	stopB := startListener(t, NewListener(env.conn, resolverB))
	defer stopB()

	// Populate both caches.
	for _, r := range []*Resolver{resolverA, resolverB} {
		value, ok, err := r.Get(context.Background(), tt.ID, "contacts.default_country_code")
		if err != nil || !ok || value != "DE" {
			t.Fatalf("priming Get() = %q, %v, %v, want %q, true, nil", value, ok, err, "DE")
		}
	}

	// A write from a completely separate Store call (standing in for a
	// third instance, or this same instance's own admin API handler)
	// must invalidate both A and B's cached entries via NOTIFY, not just
	// whichever instance happened to issue the write.
	if err := env.store.Set(context.Background(), tt.ID, "contacts.default_country_code", "FR"); err != nil {
		t.Fatalf("second Set() error: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		_, aStale := resolverA.cachedGet(configCacheKey(tt.ID, "contacts.default_country_code"))
		_, bStale := resolverB.cachedGet(configCacheKey(tt.ID, "contacts.default_country_code"))
		if !aStale && !bStale {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("cache entries not invalidated within deadline (A stale=%v, B stale=%v)", aStale, bStale)
		}
		time.Sleep(20 * time.Millisecond)
	}

	for name, r := range map[string]*Resolver{"A": resolverA, "B": resolverB} {
		value, ok, err := r.Get(context.Background(), tt.ID, "contacts.default_country_code")
		if err != nil || !ok || value != "FR" {
			t.Fatalf("resolver %s post-invalidation Get() = %q, %v, %v, want %q, true, nil", name, value, ok, err, "FR")
		}
	}
}

func TestListener_StartStop_InvalidatesAcrossInstances(t *testing.T) {
	env := openTestEnv(t)
	tt := env.createTenant(t)
	env.createModuleConfigSchema(t, tt.Slug)

	if err := env.store.Set(context.Background(), tt.ID, "contacts.default_country_code", "DE"); err != nil {
		t.Fatalf("initial Set() error: %v", err)
	}

	resolver := NewResolver(env.store, env.tenantStore, &registry.ModuleRegistry{})
	l := NewListener(env.conn, resolver)
	ready := make(chan struct{})
	l.Start(context.Background(), sync.OnceFunc(func() { close(ready) }))
	defer l.Stop()

	select {
	case <-ready:
	case <-time.After(5 * time.Second):
		t.Fatal("listener did not become ready (LISTEN not issued) in time")
	}

	value, ok, err := resolver.Get(context.Background(), tt.ID, "contacts.default_country_code")
	if err != nil || !ok || value != "DE" {
		t.Fatalf("priming Get() = %q, %v, %v, want %q, true, nil", value, ok, err, "DE")
	}

	if err := env.store.Set(context.Background(), tt.ID, "contacts.default_country_code", "FR"); err != nil {
		t.Fatalf("second Set() error: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		value, ok, err := resolver.Get(context.Background(), tt.ID, "contacts.default_country_code")
		if err == nil && ok && value == "FR" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("Get() = %q, %v, %v, want %q, true, nil before deadline", value, ok, err, "FR")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestListener_Run_StopsCleanlyOnContextCancel(t *testing.T) {
	env := openTestEnv(t)
	resolver := NewResolver(env.store, env.tenantStore, &registry.ModuleRegistry{})
	l := NewListener(env.conn, resolver)

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	ready := make(chan struct{})
	go func() { errCh <- l.Run(ctx, func() { close(ready) }) }()

	select {
	case <-ready:
	case <-time.After(5 * time.Second):
		t.Fatal("listener did not become ready in time")
	}

	cancel()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Run() after context cancel = %v, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run() did not return after context cancellation")
	}
}
