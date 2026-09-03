package db

import "github.com/djangbahevans/goerp/sdk/go/internal/hostcall"

// dbMigrationDDLInput/Output mirror the engine's own
// internal/engine/wasm.dbMigrationDDLInput/Output field-for-field via
// matching msgpack tags — host.db.migration_ddl's wire shape
// (host-abi-reference.md §5 "host.db.migration_ddl", goerp#500).
type dbMigrationDDLInput struct {
	Op     string `msgpack:"op"`
	Table  string `msgpack:"table"`
	Column string `msgpack:"column,omitempty"`
}

type dbMigrationDDLOutput struct {
	DurationMs float64 `msgpack:"duration_ms"`
}

// MigrationDropColumn calls host.db.migration_ddl to drop table's column
// column immediately — the explicit-consent escape hatch
// migration-guide.md §4 documents for model.MigrationContext.DropColumn.
// Only callable from inside a data migration handler; the host rejects it
// otherwise (db.migration_ddl_not_in_migration_context).
func MigrationDropColumn(table, column string) error {
	return migrationDDL(dbMigrationDDLInput{Op: "drop_column", Table: table, Column: column})
}

// MigrationDropTable calls host.db.migration_ddl to drop table
// immediately — model.MigrationContext.DropTable's own escape hatch.
func MigrationDropTable(table string) error {
	return migrationDDL(dbMigrationDDLInput{Op: "drop_table", Table: table})
}

func migrationDDL(in dbMigrationDDLInput) error {
	var out dbMigrationDDLOutput
	if err := hostcall.Do(hostDBMigrationDDL, in, &out); err != nil {
		return wrapExecError(err)
	}
	return nil
}
