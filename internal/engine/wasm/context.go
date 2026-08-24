package wasm

import (
	"database/sql"
	"sync"

	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/djangbahevans/goerp/internal/engine/fieldsec"
	"github.com/djangbahevans/goerp/sdk/go/model"
	"github.com/rs/zerolog/log"
	"go.opentelemetry.io/otel/trace"
)

type ModuleContext struct {
	RequestID  string
	ModuleName string
	UserID     string
	ContactID  string
	Roles      []string
	TenantID   string
	TenantSlug string
	TraceID    string
	SpanStack  []trace.Span

	transactions map[string]*sql.Tx
	txMu         sync.Mutex
	txLimiter    *TransactionLimiter

	capabilities abi.CapabilitySet

	// modelDecls is the calling module's own declared models — host.orm
	// calls resolve a model name against this list, never against another
	// module's models (internal/engine/wasm/host_orm.go).
	modelDecls []model.ModelDeclaration

	// fieldSecRegistry is the field security registry from the registry
	// snapshot in effect when this request started, so a hot reload
	// mid-request can't change which rule a single request's host.orm
	// calls see.
	fieldSecRegistry *fieldsec.FieldSecurityRegistry
}

func NewModuleContext(requestID, moduleName, userID, contactID string, roles []string, tenantID, tenantSlug, traceID string, capabilities abi.CapabilitySet, txLimiter *TransactionLimiter, modelDecls []model.ModelDeclaration, fieldSecRegistry *fieldsec.FieldSecurityRegistry) *ModuleContext {
	return &ModuleContext{
		RequestID:        requestID,
		ModuleName:       moduleName,
		UserID:           userID,
		ContactID:        contactID,
		Roles:            roles,
		TenantID:         tenantID,
		TenantSlug:       tenantSlug,
		TraceID:          traceID,
		transactions:     make(map[string]*sql.Tx),
		capabilities:     capabilities,
		txLimiter:        txLimiter,
		modelDecls:       modelDecls,
		fieldSecRegistry: fieldSecRegistry,
	}
}

// ModelDecls returns the calling module's own declared models.
func (mc *ModuleContext) ModelDecls() []model.ModelDeclaration {
	return mc.modelDecls
}

// FieldSecRegistry returns the field security registry in effect for this
// request.
func (mc *ModuleContext) FieldSecRegistry() *fieldsec.FieldSecurityRegistry {
	return mc.fieldSecRegistry
}

// RollbackAll rolls back every transaction still open in this context and
// releases each one's TransactionLimiter slot — the dispatch-path safety net
// (invokeHandler's defer, engine-internals.md §6) drains through this, not
// through the 30s expires_at on the transaction, whether the handler
// returned normally, with an error, or via a WASM trap.
func (mc *ModuleContext) RollbackAll() {
	mc.txMu.Lock()
	txs := mc.transactions
	mc.transactions = make(map[string]*sql.Tx)
	mc.txMu.Unlock()

	for txID, tx := range txs {
		if err := tx.Rollback(); err != nil {
			log.Warn().Err(err).Str("tx_id", txID).Msg("could not roll back transaction left open by module handler")
		}
		if mc.txLimiter != nil {
			mc.txLimiter.Release()
		}
	}
}

func (mc *ModuleContext) Capabilities() abi.CapabilitySet {
	return mc.capabilities
}

// HasOpenTransaction reports whether a host.db.begin transaction is already
// open in this request context — nested transactions are not supported
// (host-abi-reference.md §5 "host.db.begin": "Calling begin while a
// transaction is open returns db.transaction_already_open").
func (mc *ModuleContext) HasOpenTransaction() bool {
	mc.txMu.Lock()
	defer mc.txMu.Unlock()

	return len(mc.transactions) > 0
}

// RegisterTransaction records a transaction host.db.begin opened, keyed by
// the tx_id handed back to the module. txID is scoped to this ModuleContext
// (one request/WASM instance) and cannot be reused across requests.
func (mc *ModuleContext) RegisterTransaction(txID string, tx *sql.Tx) {
	mc.txMu.Lock()
	defer mc.txMu.Unlock()

	mc.transactions[txID] = tx
}

// Transaction looks up a transaction previously registered under txID.
func (mc *ModuleContext) Transaction(txID string) (*sql.Tx, bool) {
	mc.txMu.Lock()
	defer mc.txMu.Unlock()

	tx, ok := mc.transactions[txID]
	return tx, ok
}

// RemoveTransaction drops txID's bookkeeping entry. Callers that acquired a
// TransactionLimiter slot for this transaction are responsible for
// releasing it — RemoveTransaction only forgets the *sql.Tx.
func (mc *ModuleContext) RemoveTransaction(txID string) {
	mc.txMu.Lock()
	defer mc.txMu.Unlock()

	delete(mc.transactions, txID)
}

// TransactionIDs returns the tx_ids of every transaction still open in this
// context, for the dispatch-path safety net (RollbackAll) to drain.
func (mc *ModuleContext) TransactionIDs() []string {
	mc.txMu.Lock()
	defer mc.txMu.Unlock()

	ids := make([]string, 0, len(mc.transactions))
	for id := range mc.transactions {
		ids = append(ids, id)
	}
	return ids
}
