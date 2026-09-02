package wasm

import (
	"database/sql"
	"sync"

	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/djangbahevans/goerp/internal/engine/computed"
	"github.com/djangbahevans/goerp/internal/engine/dataaudit"
	"github.com/djangbahevans/goerp/internal/engine/event"
	"github.com/djangbahevans/goerp/internal/engine/fieldsec"
	"github.com/djangbahevans/goerp/internal/engine/permission"
	"github.com/djangbahevans/goerp/internal/engine/searchindex"
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

	// PermissionRegistry resolves a permission name to its stable
	// bitfield index, for interpreting a ModuleContext's PermissionSet
	// (auth-internals.md §13 "Permission evaluation pipeline").
	PermissionRegistry *permission.PermissionRegistry

	// SearchIndexRegistry resolves a calling module's own bare
	// search.Query index name to its declared manifest.SearchIndex
	// (host.search.query, host-abi-reference.md §12).
	SearchIndexRegistry *searchindex.Registry
}

type ModuleContext struct {
	RequestID  string
	ModuleName string
	UserID     string
	ContactID  string
	Roles      []string

	// PermissionSet is the caller's resolved permission bitfield
	// (internal/engine/auth/authcheck.AuthContext.PermissionSet),
	// interpreted against the snapshot's PermissionRegistry — see
	// auth-internals.md §13. Empty (nil) for a context with no live
	// caller, e.g. a background workflow activity dispatch.
	PermissionSet permission.PermissionBitfield

	TenantID   string
	TenantSlug string
	TraceID    string
	SpanStack  []trace.Span

	transactions map[string]openTransaction
	txMu         sync.Mutex
	txLimiter    *TransactionLimiter

	capabilities abi.CapabilitySet

	snapshot ModuleSnapshot
}

func NewModuleContext(requestID, moduleName, userID, contactID string, roles []string, permSet permission.PermissionBitfield, tenantID, tenantSlug, traceID string, capabilities abi.CapabilitySet, txLimiter *TransactionLimiter, snapshot ModuleSnapshot) *ModuleContext {
	return &ModuleContext{
		RequestID:     requestID,
		ModuleName:    moduleName,
		UserID:        userID,
		ContactID:     contactID,
		Roles:         roles,
		PermissionSet: permSet,
		TenantID:      tenantID,
		TenantSlug:    tenantSlug,
		TraceID:       traceID,
		transactions:  make(map[string]openTransaction),
		capabilities:  capabilities,
		txLimiter:     txLimiter,
		snapshot:      snapshot,
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

// SearchIndexRegistry returns the search-index registry in effect for
// this request.
func (mc *ModuleContext) SearchIndexRegistry() *searchindex.Registry {
	return mc.snapshot.SearchIndexRegistry
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

// PermissionRegistry returns the permission registry in effect for this
// request, for interpreting PermissionSet's bitfield indices.
func (mc *ModuleContext) PermissionRegistry() *permission.PermissionRegistry {
	return mc.snapshot.PermissionRegistry
}

// RollbackAll rolls back every transaction still open in this context,
// closes each one's pinned *sql.Conn (returning it to the pool — see
// openTransaction's own doc comment for why Rollback alone isn't enough),
// and releases each one's TransactionLimiter slot — the dispatch-path
// safety net (invokeHandler's defer, engine-internals.md §6) drains
// through this, not through the 30s expires_at on the transaction,
// whether the handler returned normally, with an error, or via a WASM
// trap.
func (mc *ModuleContext) RollbackAll() {
	mc.txMu.Lock()
	txs := mc.transactions
	mc.transactions = make(map[string]openTransaction)
	mc.txMu.Unlock()

	for txID, ot := range txs {
		if err := ot.tx.Rollback(); err != nil {
			log.Warn().Err(err).Str("tx_id", txID).Msg("could not roll back transaction left open by module handler")
		}
		if err := ot.conn.Close(); err != nil {
			log.Warn().Err(err).Str("tx_id", txID).Msg("could not release connection pinned by a transaction left open by module handler")
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

// openTransaction pairs a host.db.begin transaction with the *sql.Conn
// pinned for its lifetime — host.db.begin acquires the connection via
// (*sql.DB).Conn before calling BeginTx on it (rather than calling BeginTx
// on the pool directly) specifically so RawConn can later hand a host
// function the same physical connection via the connection's own Raw()
// escape hatch (goerp#511). *sql.Tx.Commit/Rollback alone never returns a
// conn acquired this way to the pool — only closing the *sql.Conn does —
// so every path that ends a transaction must close conn, not just call
// tx.Commit()/tx.Rollback().
type openTransaction struct {
	conn *sql.Conn
	tx   *sql.Tx
}

// RegisterTransaction records a transaction host.db.begin opened — conn is
// the *sql.Conn tx was started on — keyed by the tx_id handed back to the
// module. txID is scoped to this ModuleContext (one request/WASM instance)
// and cannot be reused across requests.
func (mc *ModuleContext) RegisterTransaction(txID string, conn *sql.Conn, tx *sql.Tx) {
	mc.txMu.Lock()
	defer mc.txMu.Unlock()

	mc.transactions[txID] = openTransaction{conn: conn, tx: tx}
}

// transactionEntry looks up txID's registered transaction under one
// lock — the shared primitive Transaction, RawConn, and
// TransactionAndConn all build on, so a caller needing both halves (as
// beginOrBorrowExecTx does, host_db_exec.go) pays one lock/lookup
// instead of two.
func (mc *ModuleContext) transactionEntry(txID string) (openTransaction, bool) {
	mc.txMu.Lock()
	defer mc.txMu.Unlock()

	ot, ok := mc.transactions[txID]
	return ot, ok
}

// Transaction looks up a transaction previously registered under txID.
func (mc *ModuleContext) Transaction(txID string) (*sql.Tx, bool) {
	ot, ok := mc.transactionEntry(txID)
	return ot.tx, ok
}

// RawConn returns the *sql.Conn txID's transaction is pinned to — the same
// physical connection Transaction's own *sql.Tx runs on — for a host
// function that needs pgx-specific functionality database/sql doesn't
// expose (COPY, pipelining) via that connection's own Raw() call. Work
// issued through the raw handle participates in the same transaction,
// since both share one physical connection (goerp#511).
func (mc *ModuleContext) RawConn(txID string) (*sql.Conn, bool) {
	ot, ok := mc.transactionEntry(txID)
	return ot.conn, ok
}

// TransactionAndConn returns both txID's *sql.Tx and its pinned *sql.Conn
// (RawConn's own connection) from a single lock/lookup — for a caller
// like beginOrBorrowExecTx that needs both together, instead of two
// separate locked lookups via Transaction and RawConn.
func (mc *ModuleContext) TransactionAndConn(txID string) (*sql.Conn, *sql.Tx, bool) {
	ot, ok := mc.transactionEntry(txID)
	return ot.conn, ot.tx, ok
}

// RemoveTransaction drops txID's bookkeeping entry and closes its pinned
// *sql.Conn, returning it to the pool. Callers that acquired a
// TransactionLimiter slot for this transaction are still responsible for
// releasing it themselves — RemoveTransaction only forgets the
// bookkeeping entry and releases its connection-level resources.
func (mc *ModuleContext) RemoveTransaction(txID string) {
	mc.txMu.Lock()
	ot, ok := mc.transactions[txID]
	delete(mc.transactions, txID)
	mc.txMu.Unlock()

	if ok {
		if err := ot.conn.Close(); err != nil {
			log.Warn().Err(err).Str("tx_id", txID).Msg("could not release connection pinned by a finished transaction")
		}
	}
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
