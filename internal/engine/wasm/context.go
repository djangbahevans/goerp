package wasm

import (
	"database/sql"
	"sync"

	"github.com/djangbahevans/goerp/internal/engine/abi"
	"go.opentelemetry.io/otel/trace"
)

type ModuleContext struct {
	RequestID  string
	UserID     string
	ContactID  string
	Roles      []string
	TenantID   string
	TenantSlug string
	TraceID    string
	SpanStack  []trace.Span

	transactions map[string]*sql.Tx
	txMu         sync.Mutex

	capabilities abi.CapabilitySet
}

func NewModuleContext(requestID, userID, contactID string, roles []string, tenantID, tenantSlug, traceID string, capabilities abi.CapabilitySet) *ModuleContext {
	return &ModuleContext{
		RequestID:    requestID,
		UserID:       userID,
		ContactID:    contactID,
		Roles:        roles,
		TenantID:     tenantID,
		TenantSlug:   tenantSlug,
		TraceID:      traceID,
		transactions: make(map[string]*sql.Tx),
		capabilities: capabilities,
	}
}

func (mc *ModuleContext) OpenTransactions() []*sql.Tx {
	mc.txMu.Lock()
	defer mc.txMu.Unlock()

	txs := make([]*sql.Tx, 0, len(mc.transactions))
	for _, tx := range mc.transactions {
		txs = append(txs, tx)
	}
	return txs
}
