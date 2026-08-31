package engine

import "github.com/djangbahevans/goerp/sdk/go/model"

// migrationHandler is a data migration handler registered via
// OnDataMigration, keyed by its declared Handler name (the same name a
// model.DataMigration in the module's own DataMigrations list — the
// get_data_migrations export's own source — names in its own Handler
// field).
type migrationHandler func(*model.MigrationContext) error

var migrationHandlers = map[string]migrationHandler{}

// OnDataMigration registers fn to run when a data migration job named
// handler arrives — called in init(), alongside the module's own
// DataMigrations declaration and get_data_migrations export
// (migration-guide.md §4).
func OnDataMigration(handler string, fn func(*model.MigrationContext) error) {
	migrationHandlers[handler] = fn
}

// DispatchDataMigration is what a module's handle_job export calls for a
// data migration job (goerp#114's engine-side dispatch enqueues these,
// distinguished from an ordinary job by jobqueue.WASMJobArgs.IsDataMigration
// engine-side — nothing in the wire payload itself needs to carry that,
// since a module only ever has one handle_job export and this is the
// only kind of job this SDK version dispatches through it): decode the
// incoming model.MigrationJobPayload, look up the handler registered via
// OnDataMigration by its Handler name, invoke it, and return the bare
// i32 status handle_job's ABI reserves (manifest-spec.md §26) — 0
// success, any other value failure, retried by the caller per the
// dispatching job's own retry policy (identical bare-status contract to
// handle_event, internal/engine/wasm.ModuleInstance.InvokeHandleJob's own
// doc comment).
func DispatchDataMigration(ptr, length uint32) uint32 {
	buf := ReadMem(ptr, length)

	var payload model.MigrationJobPayload
	if err := unmarshal(buf, &payload); err != nil {
		return 1
	}

	handler, ok := migrationHandlers[payload.Handler]
	if !ok {
		return 1
	}

	ctx := model.NewMigrationContext(payload)
	if err := handler(ctx); err != nil {
		ctx.Log("handler failed", "error", err.Error())
		return 1
	}
	return 0
}
