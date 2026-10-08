package db

import (
	abi "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/sdk/go/internal/hostcall"
)

// MigrationDropColumn drops table's column immediately. Schema sync never
// drops columns on its own; this is the explicit opt-in behind
// model.MigrationContext.DropColumn. It is only callable from inside a
// data migration handler; elsewhere the host rejects it with
// db.migration_ddl_not_in_migration_context.
func MigrationDropColumn(table, column string) error {
	return migrationDDL(abi.DBMigrationDDLInput{Op: abi.DBMigrationDDLOpDropColumn, Table: table, Column: column})
}

// MigrationDropTable drops table immediately; the table-level counterpart
// of MigrationDropColumn behind model.MigrationContext.DropTable.
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
