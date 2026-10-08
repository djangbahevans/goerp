package wasm

import (
	"context"
	"database/sql"
	"fmt"
	"uuid"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
)

// ManagedTransaction lets engine dispatch share a transaction with module host calls.
// Only engine dispatch can finish it; module commit and rollback calls are rejected.
type ManagedTransaction struct {
	ID      string
	Tx      *sql.Tx
	Context context.Context

	mc     *ModuleContext
	cancel context.CancelFunc
}

func (r *Runtime) BeginManagedTransaction(ctx context.Context, mc *ModuleContext) (*ManagedTransaction, error) {
	if mc.HasOpenTransaction() {
		return nil, &abiv1.HostError{Code: abiv1.ErrCodeTransactionAlreadyOpen, Message: "a transaction is already open in this request context"}
	}

	if !r.txLimiter.TryAcquire() {
		return nil, &abiv1.HostError{Code: abiv1.ErrCodeTransactionLimitExceeded, Message: "maximum concurrent transactions reached", Retry: true}
	}

	ctx, cancel := context.WithTimeout(ctx, transactionExpiry)
	if r.primaryDB == nil {
		cancel()
		r.txLimiter.Release()
		return nil, fmt.Errorf("managed transaction requires the primary database")
	}

	conn, err := r.primaryDB.Conn(ctx)
	if err != nil {
		cancel()
		r.txLimiter.Release()
		return nil, fmt.Errorf("pin managed transaction connection: %w", err)
	}

	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		_ = conn.Close()
		cancel()
		r.txLimiter.Release()
		return nil, fmt.Errorf("begin managed transaction: %w", err)
	}

	if err := applyTenantScope(ctx, tx, mc); err != nil {
		_ = tx.Rollback()
		_ = conn.Close()
		cancel()
		r.txLimiter.Release()
		return nil, fmt.Errorf("scope managed transaction: %w", err)
	}

	id := uuid.New().String()
	mc.txMu.Lock()
	mc.transactions[id] = openTransaction{conn: conn, tx: tx, managed: true}
	mc.txMu.Unlock()

	return &ManagedTransaction{
		ID:      id,
		Tx:      tx,
		Context: ctx,
		mc:      mc,
		cancel:  cancel,
	}, nil
}

func (mt *ManagedTransaction) Commit() error {
	defer mt.cancel()

	if _, ok := mt.mc.Transaction(mt.ID); !ok {
		return sql.ErrTxDone
	}

	afterCommit := mt.mc.afterCommitHooks(mt.ID)
	err := mt.Tx.Commit()
	mt.finish()
	if err != nil {
		return err
	}

	for _, fn := range afterCommit {
		fn(mt.Context)
	}

	return nil
}

func (mt *ManagedTransaction) Rollback() {
	defer mt.cancel()

	if _, ok := mt.mc.Transaction(mt.ID); !ok {
		return
	}

	_ = mt.Tx.Rollback()
	mt.finish()
}

func (mt *ManagedTransaction) finish() {
	mt.mc.RemoveTransaction(mt.ID)
	mt.mc.txLimiter.Release()
}

func managedTransactionError() *abiv1.HostError {
	return &abiv1.HostError{Code: abiv1.ErrCodeTransactionManaged, Message: "the engine owns this transaction's commit and rollback"}
}
