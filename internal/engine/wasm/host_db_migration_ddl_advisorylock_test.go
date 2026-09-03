package wasm

import (
	"testing"

	"github.com/djangbahevans/goerp/internal/engine/schema"
)

// TestMigrationDDLAdvisoryLockKeys_MatchesSchemaPackage pins
// host_db_migration_ddl.go's local migrationDDLAdvisoryLockKeys against
// schema.AdvisoryLockKeys — the two must never diverge, since
// host.db.migration_ddl has to take the identical lock BeginSync itself
// takes for the same (tenant, module) pair to actually serialize against
// it. Duplicated locally rather than imported (see
// migrationDDLAdvisoryLockKeys's own doc comment for the import-cycle
// reason); this test is what keeps the duplication honest.
func TestMigrationDDLAdvisoryLockKeys_MatchesSchemaPackage(t *testing.T) {
	cases := []struct {
		tenantSlug, moduleName string
	}{
		{"acmecorp", "contacts"},
		{"", ""},
		{"tenant-with-dashes", "module_with_underscores"},
	}
	for _, c := range cases {
		gotA, gotB := migrationDDLAdvisoryLockKeys(c.tenantSlug, c.moduleName)
		wantA, wantB := schema.AdvisoryLockKeys(c.tenantSlug, c.moduleName)
		if gotA != wantA || gotB != wantB {
			t.Errorf("migrationDDLAdvisoryLockKeys(%q, %q) = (%d, %d), want (%d, %d) (schema.AdvisoryLockKeys)",
				c.tenantSlug, c.moduleName, gotA, gotB, wantA, wantB)
		}
	}
}
