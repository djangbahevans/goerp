package db

import (
	"errors"
	"fmt"

	"github.com/djangbahevans/goerp/sdk/go/internal/hostcall"
)

// defaultLockTimeoutMs is Lock's blocking-wait cap — host-abi-reference.md's
// own documented default for host.db.lock's timeout_ms.
const defaultLockTimeoutMs = 5000

// ErrLockTimeout is returned by Lock when the advisory lock isn't
// acquired within defaultLockTimeoutMs — a plain sentinel, since
// host.db.lock reports this as Acquired: false on an otherwise
// successful call, not as a host error.
var ErrLockTimeout = errors.New("db: lock not acquired within timeout")

// IsLockTimeout reports whether err is (or wraps) ErrLockTimeout.
func IsLockTimeout(err error) bool { return errors.Is(err, ErrLockTimeout) }

// dbLockInput/dbLockOutput mirror host.db.lock's own wire shape
// (internal/engine/wasm/host_db_lock.go), duplicated rather than
// imported per this package's own convention (compiles into a module's
// own wasip1 binary).
type dbLockInput struct {
	Key       string `msgpack:"key"`
	TxID      string `msgpack:"tx_id"`
	TimeoutMs int64  `msgpack:"timeout_ms"`
}

type dbLockOutput struct {
	Acquired   bool    `msgpack:"acquired"`
	DurationMs float64 `msgpack:"duration_ms"`
}

// Lock acquires a Postgres advisory lock scoped to tx via host.db.lock,
// blocking up to defaultLockTimeoutMs. Released automatically when tx
// commits or rolls back — no explicit unlock call.
func (tx *Tx) Lock(key string) error {
	acquired, err := tx.lock(key, defaultLockTimeoutMs)
	if err != nil {
		return err
	}
	if !acquired {
		return fmt.Errorf("db: lock %q: %w", key, ErrLockTimeout)
	}
	return nil
}

// TryLock is Lock's non-blocking variant: returns (false, nil) — not an
// error — when the lock is currently held elsewhere.
func (tx *Tx) TryLock(key string) (bool, error) {
	return tx.lock(key, 0)
}

func (tx *Tx) lock(key string, timeoutMs int64) (bool, error) {
	var out dbLockOutput
	in := dbLockInput{Key: key, TxID: tx.id, TimeoutMs: timeoutMs}
	if err := hostcall.Do(hostDBLock, in, &out); err != nil {
		return false, err
	}
	return out.Acquired, nil
}
