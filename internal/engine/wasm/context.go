package wasm

import (
	"database/sql"
	"sync"

	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/djangbahevans/goerp/internal/engine/computed"
	"github.com/djangbahevans/goerp/internal/engine/dataaudit"
	"github.com/djangbahevans/goerp/internal/engine/event"
	"github.com/djangbahevans/goerp/internal/engine/fieldsec"
	"github.com/djangbahevans/goerp/sdk/go/model"
	"github.com/rs/zerolog/log"
	"go.opentelemetry.io/otel/trace"
)

// ComputeTarget bundles what's needed to invoke a module's .Computed()
// functions from outside that module's own request — its instance pool
// to borrow a fresh WASM instance from, its own declared capabilities
// (for any host.orm call the compute function itself makes), and its own
// declared models (for resolveModel to succeed inside that nested call).
// One ComputeTarget exists per loaded module, not per calling module, so
// a computed field's owning module is reachable regardless of which
// module's write triggered the recompute (host_orm_write.go's
// Many2One-hop case, go-sdk-reference.md §22).
type ComputeTarget struct {
	Pool         *InstancePool
	Capabilities abi.CapabilitySet
	ModelDecls   []model.ModelDeclaration
}

// ModuleSnapshot bundles the pieces of a registry snapshot a host function
// needs, captured once at ModuleContext construction time so a hot reload
// mid-request can't change what a single request's host.* calls see.
// Extend this struct for new registry-derived data, rather than growing
// NewModuleContext's own positional parameter list further.
type ModuleSnapshot struct {
	// ModelDecls is the calling module's own declared models — host.orm
	// calls resolve a model name against this list, never against another
	// module's models (internal/engine/wasm/host_orm.go).
	ModelDecls []model.ModelDeclaration

	// FieldSecRegistry is the field security registry in effect for this
	// request.
	FieldSecRegistry *fieldsec.FieldSecurityRegistry

	// EventRegistry is the event registry in effect for this request —
	// host.event.emit_tx validates a caller's event name against it.
	EventRegistry *event.EventRegistry

	// ComputedIndex is the reverse-dependency index (internal/engine/computed)
	// host.orm's write/read halves consult to find which computed fields
	// need to recompute after a write, or fresh on a read.
	ComputedIndex *computed.Index

	// ComputeTargets holds one ComputeTarget per loaded module (keyed by
	// module name), letting host.orm's write/read halves invoke a
	// computed field's compute function regardless of which module owns
	// it — always a fresh instance, never the currently-executing one.
	ComputeTargets map[string]ComputeTarget

	// DataAuditRegistry is the audited-tables reverse lookup
	// (internal/engine/dataaudit, manifest-spec.md §19 "Audited Tables")
	// host.orm's write path consults to find whether a just-created/
	// written/unlinked model's table needs an audit_log row.
	DataAuditRegistry *dataaudit.Registry
}

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

	snapshot ModuleSnapshot
}

func NewModuleContext(requestID, moduleName, userID, contactID string, roles []string, tenantID, tenantSlug, traceID string, capabilities abi.CapabilitySet, txLimiter *TransactionLimiter, snapshot ModuleSnapshot) *ModuleContext {
	return &ModuleContext{
		RequestID:    requestID,
		ModuleName:   moduleName,
		UserID:       userID,
		ContactID:    contactID,
		Roles:        roles,
		TenantID:     tenantID,
		TenantSlug:   tenantSlug,
		TraceID:      traceID,
		transactions: make(map[string]*sql.Tx),
		capabilities: capabilities,
		txLimiter:    txLimiter,
		snapshot:     snapshot,
	}
}

// ModelDecls returns the calling module's own declared models.
func (mc *ModuleContext) ModelDecls() []model.ModelDeclaration {
	return mc.snapshot.ModelDecls
}

// FieldSecRegistry returns the field security registry in effect for this
// request.
func (mc *ModuleContext) FieldSecRegistry() *fieldsec.FieldSecurityRegistry {
	return mc.snapshot.FieldSecRegistry
}

// EventRegistry returns the event registry in effect for this request.
func (mc *ModuleContext) EventRegistry() *event.EventRegistry {
	return mc.snapshot.EventRegistry
}

// ComputedIndex returns the computed-field reverse-dependency index in
// effect for this request.
func (mc *ModuleContext) ComputedIndex() *computed.Index {
	return mc.snapshot.ComputedIndex
}

// ComputeTargets returns the per-module compute-dispatch targets in
// effect for this request.
func (mc *ModuleContext) ComputeTargets() map[string]ComputeTarget {
	return mc.snapshot.ComputeTargets
}

// DataAuditRegistry returns the audited-tables reverse lookup in effect
// for this request.
func (mc *ModuleContext) DataAuditRegistry() *dataaudit.Registry {
	return mc.snapshot.DataAuditRegistry
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
