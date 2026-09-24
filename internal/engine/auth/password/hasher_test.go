package password

import (
	"context"
	"errors"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alexedwards/argon2id"
)

func TestNewHasher_SizesFromMemoryBudget(t *testing.T) {
	cases := []struct {
		budgetMB int
		want     int
	}{
		{budgetMB: 4096, want: 64},
		{budgetMB: 1024, want: 16},
		{budgetMB: 100, want: 1},
		{budgetMB: 0, want: 1},
	}
	for _, c := range cases {
		if got := NewHasher(c.budgetMB, time.Millisecond).Capacity(); got != c.want {
			t.Errorf("NewHasher(%d).Capacity() = %d, want %d", c.budgetMB, got, c.want)
		}
	}
}

func TestAcquire_ReturnsErrOverloadedAfterWaitWhenFull(t *testing.T) {
	h := NewHasher(64, 50*time.Millisecond)
	held, err := h.Acquire(t.Context())
	if err != nil {
		t.Fatalf("first Acquire() error: %v", err)
	}
	defer held.Release()

	start := time.Now()
	_, err = h.Acquire(t.Context())
	if !errors.Is(err, ErrOverloaded) {
		t.Fatalf("Acquire() on a full hasher = %v, want ErrOverloaded", err)
	}
	if elapsed := time.Since(start); elapsed < 50*time.Millisecond {
		t.Errorf("Acquire() gave up after %v, want it to wait the full 50ms", elapsed)
	}
}

func TestAcquire_GetsASlotFreedWithinTheWait(t *testing.T) {
	h := NewHasher(64, time.Second)
	held, err := h.Acquire(t.Context())
	if err != nil {
		t.Fatalf("first Acquire() error: %v", err)
	}
	time.AfterFunc(20*time.Millisecond, held.Release)

	slot, err := h.Acquire(t.Context())
	if err != nil {
		t.Fatalf("Acquire() after a release = %v, want a slot", err)
	}
	slot.Release()
}

func TestAcquire_StopsWaitingWhenTheContextEnds(t *testing.T) {
	h := NewHasher(64, time.Minute)
	held, err := h.Acquire(t.Context())
	if err != nil {
		t.Fatalf("first Acquire() error: %v", err)
	}
	defer held.Release()

	ctx, cancel := context.WithCancel(t.Context())
	time.AfterFunc(20*time.Millisecond, cancel)
	if _, err := h.Acquire(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Acquire() with a canceled context = %v, want context.Canceled", err)
	}
}

func TestSlotRelease_IsIdempotent(t *testing.T) {
	h := NewHasher(128, 10*time.Millisecond)
	a, err := h.Acquire(t.Context())
	if err != nil {
		t.Fatalf("Acquire() error: %v", err)
	}
	b, err := h.Acquire(t.Context())
	if err != nil {
		t.Fatalf("Acquire() error: %v", err)
	}
	a.Release()
	a.Release()

	// b is still held: a double release of a mustn't have freed b's slot
	// too, so only one more slot is available.
	c, err := h.Acquire(t.Context())
	if err != nil {
		t.Fatalf("Acquire() after releasing a = %v, want a slot", err)
	}
	defer c.Release()
	defer b.Release()
	if _, err := h.Acquire(t.Context()); !errors.Is(err, ErrOverloaded) {
		t.Fatalf("Acquire() with both slots held = %v, want ErrOverloaded", err)
	}
}

func TestAcquire_NeverExceedsCapacity(t *testing.T) {
	h := NewHasher(128, time.Second)
	var mu sync.Mutex
	var inFlight, peak int
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			slot, err := h.Acquire(t.Context())
			if err != nil {
				t.Errorf("Acquire() error: %v", err)
				return
			}
			defer slot.Release()
			mu.Lock()
			inFlight++
			peak = max(peak, inFlight)
			mu.Unlock()
			time.Sleep(10 * time.Millisecond)
			mu.Lock()
			inFlight--
			mu.Unlock()
		})
	}
	wg.Wait()
	if peak > 2 {
		t.Errorf("peak concurrent slots = %d, want at most 2", peak)
	}
}

func TestSlot_HashUsesDocumentedParams(t *testing.T) {
	slot, err := NewHasher(64, time.Second).Acquire(t.Context())
	if err != nil {
		t.Fatalf("Acquire() error: %v", err)
	}
	defer slot.Release()

	h, err := slot.Hash("correct horse battery staple")
	if err != nil {
		t.Fatalf("Hash() error: %v", err)
	}
	match, params, err := argon2id.CheckHash("correct horse battery staple", h)
	if err != nil || !match {
		t.Fatalf("CheckHash() = %v, %v, want match", match, err)
	}
	if *params != *ArgonParams {
		t.Errorf("params = %+v, want %+v", *params, *ArgonParams)
	}
}

func TestSlot_Verify(t *testing.T) {
	slot, err := NewHasher(64, time.Second).Acquire(t.Context())
	if err != nil {
		t.Fatalf("Acquire() error: %v", err)
	}
	defer slot.Release()

	current, err := slot.Hash("correct horse battery staple")
	if err != nil {
		t.Fatalf("Hash() error: %v", err)
	}
	outdated, err := argon2id.CreateHash("correct horse battery staple", &argon2id.Params{
		Memory: 16 * 1024, Iterations: 1, Parallelism: 1, SaltLength: 16, KeyLength: 32,
	})
	if err != nil {
		t.Fatalf("CreateHash() error: %v", err)
	}

	cases := []struct {
		name            string
		plain, hash     string
		wantMatch       bool
		wantNeedsRehash bool
	}{
		{name: "current params", plain: "correct horse battery staple", hash: current, wantMatch: true},
		{name: "outdated params", plain: "correct horse battery staple", hash: outdated, wantMatch: true, wantNeedsRehash: true},
		{name: "wrong password", plain: "wrong", hash: outdated},
	}
	for _, c := range cases {
		match, needsRehash, err := slot.Verify(c.plain, c.hash)
		if err != nil {
			t.Fatalf("%s: Verify() error: %v", c.name, err)
		}
		if match != c.wantMatch || needsRehash != c.wantNeedsRehash {
			t.Errorf("%s: Verify() = %v, %v, want %v, %v", c.name, match, needsRehash, c.wantMatch, c.wantNeedsRehash)
		}
	}
}

// Every Argon2id operation must go through a Slot, so nothing outside this
// package may import argon2id directly (tests excepted: they seed hashes).
func TestOnlyThisPackageImportsArgon2id(t *testing.T) {
	root := filepath.Join("..", "..", "..", "..")
	var offenders []string
	for _, dir := range []string{"cmd", "internal", "sdk"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return err
			}
			f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
			if err != nil {
				return err
			}
			for _, imp := range f.Imports {
				if imp.Path.Value == `"github.com/alexedwards/argon2id"` {
					offenders = append(offenders, path)
				}
			}
			return nil
		})
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("walk %s: %v", dir, err)
		}
	}
	self, err := filepath.Abs(".")
	if err != nil {
		t.Fatalf("Abs() error: %v", err)
	}
	for _, path := range offenders {
		abs, err := filepath.Abs(path)
		if err != nil {
			t.Fatalf("Abs() error: %v", err)
		}
		if filepath.Dir(abs) != self {
			t.Errorf("%s imports argon2id directly; use password.Hasher", path)
		}
	}
}
