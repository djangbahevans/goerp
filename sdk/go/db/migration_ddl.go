package db

import (
	abi "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/sdk/go/internal/hostcall"
)

// MigrationDropColumn calls host.db.migration_ddl to drop table's column
// column immediately — the explicit-consent escape hatch
// migration-guide.md §4 documents for model.MigrationContext.DropColumn.
// Only callable from inside a data migration handler; the host rejects it
// otherwise (db.migration_ddl_not_in_migration_context).
func MigrationDropColumn(table, column string) error {
	return migrationDDL(abi.DBMigrationDDLInput{Op: abi.DBMigrationDDLOpDropColumn, Table: table, Column: column})
}

// MigrationDropTable calls host.db.migration_ddl to drop table
// immediately — model.MigrationContext.DropTable's own escape hatch.
func MigrationDropTable(table string) error {
	return migrationDDL(abi.DBMigrationDDLInput{Op: abi.DBMigrationDDLOpDropTable, Table: table})
}

func migrationDDL(in abi.DBMigrationDDLInput) error {
	var out abi.DBMigrationDDLOutput
	if err := hostcall.Do(hostDBMigrationDDL, in, &out); err != nil {
		return wrapExecError(err)
	}
	return nil
}
