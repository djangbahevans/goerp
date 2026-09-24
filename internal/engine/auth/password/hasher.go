package password

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/alexedwards/argon2id"
)

// ErrOverloaded means no Argon2id slot freed up within the acquire wait.
// Handlers answer 503 with Retry-After, never 401: the credentials were
// never checked.
var ErrOverloaded = errors.New("password: too many concurrent hash operations")

// argonMemoryMB is one Argon2id operation's memory cost (ArgonParams.Memory
// is in KiB).
const argonMemoryMB = 64

// OverloadRetryAfterSeconds is the Retry-After value sent with a 503 for
// ErrOverloaded; a slot frees within one hash time (well under a second).
const OverloadRetryAfterSeconds = 1

// Hasher bounds concurrent Argon2id operations across the whole process
// (auth-internals.md §15 "Global Argon2 verification limit"). Every
// Argon2id hash and verification runs through a Slot, so no call site can
// skip the limit.
type Hasher struct {
	slots chan struct{}
	wait  time.Duration
}

// NewHasher sizes the limit from a memory budget: budgetMB / 64, at least
// one slot. wait bounds how long Acquire queues before ErrOverloaded. It
// also builds the dummy hash up front, so the first VerifyDummy costs the
// same as every later one.
func NewHasher(budgetMB int, wait time.Duration) *Hasher {
	_, _ = dummyHash()
	return &Hasher{
		slots: make(chan struct{}, max(budgetMB/argonMemoryMB, 1)),
		wait:  wait,
	}
}

// Capacity is the maximum number of concurrent Argon2id operations.
func (h *Hasher) Capacity() int {
	return cap(h.slots)
}

// Acquire waits up to the configured wait for a free slot. The caller must
// Release the slot; releasing more than once is a no-op.
func (h *Hasher) Acquire(ctx context.Context) (*Slot, error) {
	select {
	case h.slots <- struct{}{}:
		return &Slot{release: sync.OnceFunc(func() { <-h.slots })}, nil
	default:
	}

	timer := time.NewTimer(h.wait)
	defer timer.Stop()
	select {
	case h.slots <- struct{}{}:
		return &Slot{release: sync.OnceFunc(func() { <-h.slots })}, nil
	case <-timer.C:
		return nil, ErrOverloaded
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Slot is one held unit of Argon2id capacity. Several operations may run
// on the same slot one after another (e.g. verify then re-hash on login).
type Slot struct {
	release func()
}

func (s *Slot) Release() {
	s.release()
}

// Verify checks plain against hash. needsRehash reports a match whose hash
// used parameters other than ArgonParams.
func (s *Slot) Verify(plain, hash string) (match, needsRehash bool, err error) {
	match, params, err := argon2id.CheckHash(plain, hash)
	if err != nil || !match {
		return false, false, err
	}
	return true, !paramsMatch(params, ArgonParams), nil
}

// VerifyDummy costs the same as Verify against a real hash and always
// fails, so a missing account takes as long to reject as a wrong password
// (auth-internals.md §15 "Timing attack prevention").
func (s *Slot) VerifyDummy(plain string) {
	hash, err := dummyHash()
	if err != nil {
		return
	}
	_, _ = argon2id.ComparePasswordAndHash(plain, hash)
}

func (s *Slot) Hash(plain string) (string, error) {
	return argon2id.CreateHash(plain, ArgonParams)
}

// dummyHash is built on first use rather than at package init, so importing
// this package doesn't cost an Argon2id hash.
var dummyHash = sync.OnceValues(func() (string, error) {
	return argon2id.CreateHash("timing-normalisation-dummy-password", ArgonParams)
})

func paramsMatch(got, want *argon2id.Params) bool {
	return got.Memory == want.Memory &&
		got.Iterations == want.Iterations &&
		got.Parallelism == want.Parallelism &&
		got.SaltLength == want.SaltLength &&
		got.KeyLength == want.KeyLength
}
