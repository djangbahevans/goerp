package wasm

import (
	"database/sql"
	"sync"

	"github.com/riverqueue/river"
	"github.com/tetratelabs/wazero/api"
)

// instanceRegistry maps a live wazero api.Module (the calling instance) back
// to the ModuleInstance/ModuleContext that invokeHandler attached for the
// current request — the lookup every host function closure needs to find out
// who is calling it (engine-internals.md §7).
type instanceRegistry struct {
	mu        sync.Mutex
	instances map[api.Module]*ModuleInstance
}

func newInstanceRegistry() instanceRegistry {
	return instanceRegistry{instances: make(map[api.Module]*ModuleInstance)}
}

func (r *Runtime) RegisterInstance(inst *ModuleInstance) {
	r.registry.mu.Lock()
	defer r.registry.mu.Unlock()

	r.registry.instances[inst.Module()] = inst
}

func (r *Runtime) UnregisterInstance(inst *ModuleInstance) {
	r.registry.mu.Lock()
	defer r.registry.mu.Unlock()

	delete(r.registry.instances, inst.Module())
}

func (r *Runtime) InstanceForModule(m api.Module) *ModuleInstance {
	r.registry.mu.Lock()
	defer r.registry.mu.Unlock()

	return r.registry.instances[m]
}

// TxLimiter returns the runtime's engine-wide TransactionLimiter, so
// Engine's per-request ModuleContext construction can thread it through to
// host.db.begin's usage of it.
func (r *Runtime) TxLimiter() *TransactionLimiter {
	return r.txLimiter
}

// EventInsertClient returns the same never-started river.Client[*sql.Tx]
// registerHostORM/registerHostEvent already use internally (runtime.go's
// own doc comment on its construction) — exposed so a caller outside this
// package (dispatchORMRoute, goerp#346) can pass it straight into
// ORMCreate/ORMWrite/ORMUnlink without constructing a second client.
func (r *Runtime) EventInsertClient() *river.Client[*sql.Tx] {
	return r.eventInsertClient
}
