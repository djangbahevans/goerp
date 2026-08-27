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

func TestApplyMaskPattern(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		value   any
		want    string
	}{
		{"last4 substitution", "****{last4}", "1234567890", "****7890"},
		{"first2 substitution", "{first2}****", "1234567890", "12****"},
		{"length substitution", "{length} digits", "1234567890", "10 digits"},
		{"combined tokens", "{first2}...{last4}", "1234567890", "12...7890"},
		{"shorter than last4 substitutes whole value", "****{last4}", "12", "****12"},
		{"shorter than first2 substitutes whole value", "{first2}**", "1", "1**"},
		{"non-string value returns pattern unchanged", "****{last4}", int64(12345), "****{last4}"},
		{"literal pattern with no tokens", "REDACTED", "1234567890", "REDACTED"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := applyMaskPattern(tt.pattern, tt.value); got != tt.want {
				t.Errorf("applyMaskPattern(%q, %v) = %q, want %q", tt.pattern, tt.value, got, tt.want)
			}
		})
	}
}

func TestCallerHasPermission(t *testing.T) {
	reg := permission.NewPermissionRegistry()
	reg.Register("testmodule", []manifest.Permission{{Name: "contacts:contact:financials_read"}})
	idx, _ := reg.Index("contacts:contact:financials_read")

	var granted permission.PermissionBitfield
	granted.Set(idx)

	mc := &ModuleContext{PermissionSet: granted}
	if !callerHasPermission(mc, reg, "contacts:contact:financials_read") {
		t.Error("expected caller with the permission set to satisfy it")
	}

	mcDenied := &ModuleContext{}
	if callerHasPermission(mcDenied, reg, "contacts:contact:financials_read") {
		t.Error("expected caller without the permission set to fail it")
	}

	if callerHasPermission(mc, reg, "never:registered:permission") {
		t.Error("expected an unregistered permission name to fail closed")
	}

	if callerHasPermission(mc, nil, "contacts:contact:financials_read") {
		t.Error("expected a nil PermissionRegistry to fail closed rather than panic")
	}
}

// fieldSecTestModelDecl declares three access-restricted fields, one per
// OnDeniedRead behaviour, alongside an unrestricted "name" field —
// enough to exercise every path applyFieldMasking takes in one model.
func fieldSecTestModelDecl() model.ModelDeclaration {
	return model.ModelDeclaration{
		Name:  "widget",
		Table: "widgets",
		Fields: []model.NamedField{
			{Name: "id", Def: model.UUID().Required().PrimaryKey()},
			{Name: "name", Def: model.Text().Required()},
			{Name: "credit_limit", Def: model.Integer().
				Access(model.AccessRead("contacts:contact:financials_read")).
				OnDeniedRead(model.Omit)},
			{Name: "bank_account", Def: model.Char().
				Access(model.AccessRead("hr:employee:banking_read")).
				OnDeniedRead(model.Mask("****{last4}"))},
			{Name: "notes", Def: model.Text().
				Access(model.AccessRead("contacts:contact:notes_read")).
				OnDeniedRead(model.Nullify)},
		},
	}
}

func createFieldSecFixtureTable(t *testing.T, primaryDB *sql.DB, slug, id string) {
	t.Helper()
	ctx := context.Background()
	schema := "tenant_" + slug

	if _, err := primaryDB.ExecContext(ctx, `CREATE TABLE `+schema+`.widgets (
		id UUID PRIMARY KEY,
		name TEXT NOT NULL,
		credit_limit INTEGER,
		bank_account TEXT,
		notes TEXT
	)`); err != nil {
		t.Fatalf("create widgets table: %v", err)
	}
	if _, err := primaryDB.ExecContext(ctx,
		"INSERT INTO "+schema+".widgets (id, name, credit_limit, bank_account, notes) VALUES ($1, $2, $3, $4, $5)",
		id, "Widget A", 5000, "1234567890", "internal notes"); err != nil {
		t.Fatalf("insert fixture row: %v", err)
	}
}

// newFieldSecModuleContext builds a ModuleContext with real
// FieldSecurityRegistry/PermissionRegistry wiring — grantedPermissions
// is the subset of the three declared permissions this caller
// satisfies.
func newFieldSecModuleContext(slug string, grantedPermissions ...string) *ModuleContext {
	decl := fieldSecTestModelDecl()

	fieldSecReg := fieldsec.New()
	fieldSecReg.Register("testmodule", []model.ModelDeclaration{decl})

	permReg := permission.NewPermissionRegistry()
	permReg.Register("testmodule", []manifest.Permission{
		{Name: "contacts:contact:financials_read"},
		{Name: "hr:employee:banking_read"},
		{Name: "contacts:contact:notes_read"},
	})

	var permSet permission.PermissionBitfield
	for _, name := range grantedPermissions {
		idx, ok := permReg.Index(name)
		if !ok {
			panic("newFieldSecModuleContext: unregistered permission " + name)
		}
		permSet.Set(idx)
	}

	return NewModuleContext("req-1", "testmodule", "user-1", "contact-1", []string{"admin"}, permSet,
		"tenant-id-1", slug, "trace-1", abi.CapDBRead, nil,
		ModuleSnapshot{
			ModelDecls:         []model.ModelDeclaration{decl},
			FieldSecRegistry:   fieldSecReg,
			PermissionRegistry: permReg,
		})
}

func TestORMRead_FieldSecurity_DeniedFieldsMaskedPerBehaviour(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("fieldsectest%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	id := "11111111-1111-1111-1111-111111111111"
	createFieldSecFixtureTable(t, primaryDB, slug, id)

	// No permissions granted — every restricted field should be denied.
	modCtx := newFieldSecModuleContext(slug)

	out, hostErr := ORMRead(ctx, primaryDB, nil, modCtx, ORMReadInput{Model: "testmodule.widget", IDs: []string{id}})
	if hostErr != nil {
		t.Fatalf("ORMRead: %+v", hostErr)
	}
	if len(out.Records) != 1 {
		t.Fatalf("got %d records, want 1", len(out.Records))
	}
	record := out.Records[0]

	if _, present := record["credit_limit"]; present {
		t.Errorf("credit_limit (Omit) should be absent from the response, got %v", record["credit_limit"])
	}
	if v, ok := record["bank_account"]; !ok || v != "****7890" {
		t.Errorf("bank_account (Mask) = %v, want \"****7890\"", v)
	}
	if v, present := record["notes"]; !present {
		t.Error("notes (Nullify) should still be present as a key")
	} else if v != nil {
		t.Errorf("notes (Nullify) = %v, want nil", v)
	}
	if record["name"] != "Widget A" {
		t.Errorf("name (no rule) = %v, want unaffected", record["name"])
	}
}

func TestORMRead_FieldSecurity_GrantedPermissionSeesRealValue(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("fieldsecgranted%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	id := "22222222-2222-2222-2222-222222222222"
	createFieldSecFixtureTable(t, primaryDB, slug, id)

	// Only the financials permission granted — credit_limit should come
	// through unmasked, bank_account/notes still denied.
	modCtx := newFieldSecModuleContext(slug, "contacts:contact:financials_read")

	out, hostErr := ORMRead(ctx, primaryDB, nil, modCtx, ORMReadInput{Model: "testmodule.widget", IDs: []string{id}})
	if hostErr != nil {
		t.Fatalf("ORMRead: %+v", hostErr)
	}
	record := out.Records[0]

	if got := asInt(record["credit_limit"]); got != 5000 {
		t.Errorf("credit_limit = %v, want the real value 5000", record["credit_limit"])
	}
	if v, ok := record["bank_account"]; !ok || v != "****7890" {
		t.Errorf("bank_account (still denied) = %v, want \"****7890\"", v)
	}
}

func TestORMSearchRead_FieldSecurity_DeniedFieldsMasked(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("fieldsecsearchread%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	id := "33333333-3333-3333-3333-333333333333"
	createFieldSecFixtureTable(t, primaryDB, slug, id)

	modCtx := newFieldSecModuleContext(slug)

	out, hostErr := ORMSearchRead(ctx, primaryDB, modCtx, ORMSearchReadInput{Model: "testmodule.widget", Domain: ""})
	if hostErr != nil {
		t.Fatalf("ORMSearchRead: %+v", hostErr)
	}
	if len(out.Records) != 1 {
		t.Fatalf("got %d records, want 1", len(out.Records))
	}
	if _, present := out.Records[0]["credit_limit"]; present {
		t.Error("credit_limit should be omitted from search_read results too")
	}
}

func asInt(v any) int64 {
	switch n := v.(type) {
	case int8:
		return int64(n)
	case int16:
		return int64(n)
	case int32:
		return int64(n)
	case int64:
		return n
	case int:
		return int64(n)
	default:
		return -1
	}
}
