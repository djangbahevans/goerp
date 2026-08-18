package wasm

import "sync/atomic"

// TransactionLimiter enforces the engine-wide concurrent open-transaction
// cap host-abi-reference.md's host.db.begin documents ("db.transaction_limit_exceeded").
type TransactionLimiter struct {
	max     int64
	current atomic.Int64
}

func NewTransactionLimiter(max int) *TransactionLimiter {
	return &TransactionLimiter{max: int64(max)}
}

// TryAcquire reserves one slot, returning false if the engine-wide limit is
// already reached. Every successful TryAcquire must be paired with exactly
// one Release.
func (l *TransactionLimiter) TryAcquire() bool {
	for {
		cur := l.current.Load()
		if cur >= l.max {
			return false
		}
		if l.current.CompareAndSwap(cur, cur+1) {
			return true
		}
	}
}

func (l *TransactionLimiter) Release() {
	l.current.Add(-1)
}
