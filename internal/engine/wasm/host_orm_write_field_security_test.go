package wasm

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/djangbahevans/goerp/internal/engine/fieldsec"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/permission"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

// writeFieldSecTestModelDecl declares two write-restricted fields, one
// per OnDeniedWrite behaviour, alongside an unrestricted "name" field.
func writeFieldSecTestModelDecl() model.ModelDeclaration {
	return model.ModelDeclaration{
		Name:  "widget",
		Table: "widgets",
		Fields: []model.NamedField{
			{Name: "id", Def: model.UUID().Required().PrimaryKey()},
			{Name: "name", Def: model.Text().Required()},
			{Name: "discount_percent", Def: model.Integer().
				Access(model.AccessWrite("sales:order:set_discount")).
				OnDeniedWrite(model.Reject)},
			{Name: "internal_flag", Def: model.Boolean().
				Access(model.AccessWrite("sales:order:set_internal_flag")).
				OnDeniedWrite(model.Ignore)},
		},
	}
}

func createWriteFieldSecFixtureTable(t *testing.T, primaryDB *sql.DB, slug string) {
	t.Helper()
	ctx := context.Background()
	schema := "tenant_" + slug

	if _, err := primaryDB.ExecContext(ctx, `CREATE TABLE `+schema+`.widgets (
		id UUID PRIMARY KEY,
		name TEXT NOT NULL,
		discount_percent INTEGER,
		internal_flag BOOLEAN
	)`); err != nil {
		t.Fatalf("create widgets table: %v", err)
	}
}

// newWriteFieldSecModuleContext builds a ModuleContext with real
// FieldSecurityRegistry/PermissionRegistry wiring for the write-side
// model — grantedPermissions is the subset of the two declared
// permissions this caller satisfies.
func newWriteFieldSecModuleContext(slug string, grantedPermissions ...string) *ModuleContext {
	decl := writeFieldSecTestModelDecl()

	fieldSecReg := fieldsec.New()
	fieldSecReg.Register("testmodule", []model.ModelDeclaration{decl})

	permReg := permission.NewPermissionRegistry()
	permReg.Register("testmodule", []manifest.Permission{
		{Name: "sales:order:set_discount"},
		{Name: "sales:order:set_internal_flag"},
	})

	var permSet permission.PermissionBitfield
	for _, name := range grantedPermissions {
		idx, ok := permReg.Index(name)
		if !ok {
			panic("newWriteFieldSecModuleContext: unregistered permission " + name)
		}
		permSet.Set(idx)
	}

	return NewModuleContext("req-1", "testmodule", "user-1", "contact-1", []string{"admin"}, permSet,
		"tenant-id-1", slug, "trace-1", abi.CapDBWrite, nil,
		ModuleSnapshot{
			ModelDecls:         []model.ModelDeclaration{decl},
			FieldSecRegistry:   fieldSecReg,
			PermissionRegistry: permReg,
		})
}

func TestORMCreate_FieldSecurity_RejectDeniesEntireRequest(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("writefieldsecreject%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	createWriteFieldSecFixtureTable(t, primaryDB, slug)

	r := newHostDBTestRuntime(t, primaryDB, 10)
	mc := newWriteFieldSecModuleContext(slug) // no permissions granted
	inst := newHostORMWriteCaller(t, ctx, r, mc)

	id := "11111111-1111-1111-1111-111111111111"
	env := callORMHost(t, ctx, inst, "call_create", ORMCreateInput{
		Model:  "testmodule.widget",
		Record: map[string]any{"id": id, "name": "Widget A", "discount_percent": int64(10)},
	}, nil)
	if env.OK {
		t.Fatal("expected create to be rejected for the write-denied discount_percent field")
	}
	if env.Error.Code != abi.ErrCodeFieldWriteDenied {
		t.Errorf("Error.Code = %q, want %q", env.Error.Code, abi.ErrCodeFieldWriteDenied)
	}
	if env.Error.Details["field"] != "discount_percent" {
		t.Errorf("Error.Details[field] = %v, want discount_percent", env.Error.Details["field"])
	}

	var count int
	if err := primaryDB.QueryRow("SELECT count(*) FROM tenant_"+slug+".widgets WHERE id = $1", id).Scan(&count); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if count != 0 {
		t.Errorf("expected no row to have been created, found %d", count)
	}
}

func TestORMCreate_FieldSecurity_IgnoreStripsFieldSilently(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("writefieldsecignore%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	createWriteFieldSecFixtureTable(t, primaryDB, slug)

	r := newHostDBTestRuntime(t, primaryDB, 10)
	mc := newWriteFieldSecModuleContext(slug) // no permissions granted
	inst := newHostORMWriteCaller(t, ctx, r, mc)

	id := "22222222-2222-2222-2222-222222222222"
	var out ORMCreateOutput
	env := callORMHost(t, ctx, inst, "call_create", ORMCreateInput{
		Model:  "testmodule.widget",
		Record: map[string]any{"id": id, "name": "Widget B", "internal_flag": true},
	}, &out)
	if !env.OK {
		t.Fatalf("create failed: %+v", env.Error)
	}
	// RETURNING * always includes every column (NULL if never written), so
	// the key itself is present — what matters is the column was never
	// set to the requested value.
	if out.Record["internal_flag"] != nil {
		t.Errorf("internal_flag = %v, want nil (silently stripped, never written)", out.Record["internal_flag"])
	}

	var flag sql.NullBool
	if err := primaryDB.QueryRow("SELECT internal_flag FROM tenant_"+slug+".widgets WHERE id = $1", id).Scan(&flag); err != nil {
		t.Fatalf("query row: %v", err)
	}
	if flag.Valid {
		t.Errorf("internal_flag column = %v, want NULL (never written)", flag.Bool)
	}
}

func TestORMCreate_FieldSecurity_GrantedPermissionWritesNormally(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("writefieldsecgranted%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	createWriteFieldSecFixtureTable(t, primaryDB, slug)

	r := newHostDBTestRuntime(t, primaryDB, 10)
	mc := newWriteFieldSecModuleContext(slug, "sales:order:set_discount")
	inst := newHostORMWriteCaller(t, ctx, r, mc)

	id := "33333333-3333-3333-3333-333333333333"
	var out ORMCreateOutput
	env := callORMHost(t, ctx, inst, "call_create", ORMCreateInput{
		Model:  "testmodule.widget",
		Record: map[string]any{"id": id, "name": "Widget C", "discount_percent": int64(15)},
	}, &out)
	if !env.OK {
		t.Fatalf("create failed: %+v", env.Error)
	}
	if got := asInt(out.Record["discount_percent"]); got != 15 {
		t.Errorf("discount_percent = %v, want the real value 15", out.Record["discount_percent"])
	}
}

func TestORMWrite_FieldSecurity_RejectDeniesEntireRequest(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("writefieldsecwritereject%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	createWriteFieldSecFixtureTable(t, primaryDB, slug)

	r := newHostDBTestRuntime(t, primaryDB, 10)
	// Create with a granted caller first so there's a row to update.
	granted := newWriteFieldSecModuleContext(slug, "sales:order:set_discount")
	createInst := newHostORMWriteCaller(t, ctx, r, granted)

	id := "44444444-4444-4444-4444-444444444444"
	callORMHost(t, ctx, createInst, "call_create", ORMCreateInput{
		Model:  "testmodule.widget",
		Record: map[string]any{"id": id, "name": "Widget D", "discount_percent": int64(5)},
	}, nil)

	// Now write with a caller lacking the permission — the same
	// buildAssignment chokepoint should reject this too, proving the
	// enforcement isn't create-only.
	denied := newWriteFieldSecModuleContext(slug)
	writeInst := newHostORMWriteCaller(t, ctx, r, denied)

	env := callORMHost(t, ctx, writeInst, "call_write", ORMWriteInput{
		Model:  "testmodule.widget",
		ID:     id,
		Record: map[string]any{"discount_percent": int64(50)},
	}, nil)
	if env.OK {
		t.Fatal("expected write to be rejected for the write-denied discount_percent field")
	}
	if env.Error.Code != abi.ErrCodeFieldWriteDenied {
		t.Errorf("Error.Code = %q, want %q", env.Error.Code, abi.ErrCodeFieldWriteDenied)
	}

	var discount int
	if err := primaryDB.QueryRow("SELECT discount_percent FROM tenant_"+slug+".widgets WHERE id = $1", id).Scan(&discount); err != nil {
		t.Fatalf("query row: %v", err)
	}
	if discount != 5 {
		t.Errorf("discount_percent = %d, want unchanged at 5", discount)
	}
}
