package wasm

import (
	"context"
	"fmt"
	"testing"
	"time"
	"uuid"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/schema"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

func TestExtensionMigrationDDLUsesBaseModelAndLock(t *testing.T) {
	conn := openTestPrimaryDB(t)
	slug := fmt.Sprintf("extmigration%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, conn, slug)
	if _, err := conn.ExecContext(t.Context(), "CREATE TABLE tenant_"+slug+".contacts (id UUID PRIMARY KEY, industry TEXT)"); err != nil {
		t.Fatal(err)
	}
	decl := *model.Define("contact", model.Table("contacts")).Field("id", model.UUID().PrimaryKey())
	mc := NewModuleContext("extension-migration", "industry", "", "", nil, nil, uuid.NewV7().String(), slug, "", abi.CapDBMigrationDDL, nil, ModuleSnapshot{
		ExtendsModels:  []string{"contacts.contact"},
		ComputeTargets: map[string]ComputeTarget{"contacts": {ModelDecls: []model.ModelDeclaration{decl}}},
	})
	mc.IsDataMigrationJob = true

	pool := schema.NewPool(conn, time.Second)
	sess, err := pool.BeginSync(t.Context(), mc.TenantID, slug, "contacts", &manifest.Manifest{Name: "contacts", Type: "domain"})
	if err != nil {
		t.Fatal(err)
	}

	input := abiv1.DBMigrationDDLInput{Op: abiv1.DBMigrationDDLOpDropColumn, Table: "contacts", Column: "industry"}
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	_, hostErr := DBMigrationDDL(ctx, conn, mc, input)
	if err := sess.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if hostErr == nil || hostErr.Code != abiv1.ErrCodeDBTimeout {
		t.Fatalf("migration ignores base sync lock: %+v", hostErr)
	}

	if _, hostErr := DBMigrationDDL(t.Context(), conn, mc, input); hostErr != nil {
		t.Fatalf("extension cleanup without redeclaring base model: %+v", hostErr)
	}
	var exists bool
	if err := conn.QueryRowContext(t.Context(), `SELECT EXISTS (
		SELECT 1 FROM information_schema.columns WHERE table_schema = $1 AND table_name = 'contacts' AND column_name = 'industry'
	)`, "tenant_"+slug).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Fatal("extension migration did not remove the orphaned field")
	}
}
