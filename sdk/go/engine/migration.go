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
// handler arrives. Call it in init(), alongside the module's
// DataMigrations declaration and get_data_migrations export.
func OnDataMigration(handler string, fn func(*model.MigrationContext) error) {
	migrationHandlers[handler] = fn
}
