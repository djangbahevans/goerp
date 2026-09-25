package wasm

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/djangbahevans/goerp/internal/engine/fieldsec"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/permission"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

const (
	crossModuleCustomerID = "22222222-2222-2222-2222-222222222222"
	crossModuleOrderID    = "33333333-3333-3333-3333-333333333333"
	crossModuleReadPerm   = "contacts:contact:name_read"
)

func crossModuleContactDecl(displayName model.FieldDef) model.ModelDeclaration {
	return model.ModelDeclaration{
		Name:  "contact",
		Table: "contacts",
		Fields: []model.NamedField{
			{Name: "id", Def: model.UUID().Required().PrimaryKey()},
			{Name: "display_name", Def: displayName},
		},
	}
}

func crossModuleOrderDecl(relatedModel string) model.ModelDeclaration {
	return model.ModelDeclaration{
		Name:  "order",
		Table: "orders",
		Fields: []model.NamedField{
			{Name: "id", Def: model.UUID().Required().PrimaryKey()},
			{Name: "customer_id", Def: model.Many2One(relatedModel)},
		},
	}
}

func createCrossModuleFixture(t *testing.T, slug string) *sql.DB {
	t.Helper()
	primaryDB := openTestPrimaryDB(t)
	ctx := t.Context()
	createFixtureTenantSchema(t, primaryDB, slug)
	schemaName := "tenant_" + slug

	stmts := []string{
		`CREATE TABLE ` + schemaName + `.contacts (id UUID PRIMARY KEY, display_name TEXT NOT NULL)`,
		`CREATE TABLE ` + schemaName + `.orders (id UUID PRIMARY KEY, customer_id UUID)`,
		`INSERT INTO ` + schemaName + `.contacts (id, display_name) VALUES ('` + crossModuleCustomerID + `', 'Acme Corp')`,
		`INSERT INTO ` + schemaName + `.orders (id, customer_id) VALUES ('` + crossModuleOrderID + `', '` + crossModuleCustomerID + `')`,
	}
	for _, stmt := range stmts {
		if _, err := primaryDB.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("fixture %q: %v", stmt, err)
		}
	}
	return primaryDB
}

// newCrossModuleContext builds a "sales" module context whose order model
// relates to contactDecl, a model owned by the loaded "contacts" module.
// A nil contactDecl leaves "contacts" out of the loaded modules.
func newCrossModuleContext(slug string, contactDecl *model.ModelDeclaration, granted bool) *ModuleContext {
	snapshot := ModuleSnapshot{
		ModelDecls:     []model.ModelDeclaration{crossModuleOrderDecl("contacts.contact")},
		ComputeTargets: map[string]ComputeTarget{},
	}

	if contactDecl != nil {
		snapshot.ComputeTargets["contacts"] = ComputeTarget{ModelDecls: []model.ModelDeclaration{*contactDecl}}

		fieldSecReg := fieldsec.New()
		fieldSecReg.Register("contacts", []model.ModelDeclaration{*contactDecl})
		snapshot.FieldSecRegistry = fieldSecReg

		permReg := permission.NewPermissionRegistry()
		permReg.Register("contacts", []manifest.Permission{{Name: crossModuleReadPerm}})
		snapshot.PermissionRegistry = permReg
	}

	var permSet permission.PermissionBitfield
	if granted && snapshot.PermissionRegistry != nil {
		idx, _ := snapshot.PermissionRegistry.Index(crossModuleReadPerm)
		permSet.Set(idx)
	}

	return NewModuleContext("req-1", "sales", "user-1", "contact-1", []string{"admin"}, permSet,
		"tenant-id-1", slug, "trace-1", abi.CapDBRead, nil, snapshot)
}

func searchReadCustomer(t *testing.T, ctx context.Context, mc *ModuleContext, primaryDB *sql.DB) map[string]any {
	t.Helper()
	r := newHostDBTestRuntime(t, primaryDB, 10)
	inst := newHostORMCaller(t, ctx, r, mc)

	var out abiv1.ORMSearchReadOutput
	env := callORMHost(t, ctx, inst, "call_search_read", abiv1.ORMSearchReadInput{Model: "sales.order"}, &out)
	if !env.OK {
		t.Fatalf("search_read failed: %+v", env.Error)
	}
	if len(out.Records) != 1 {
		t.Fatalf("got %d records, want 1", len(out.Records))
	}
	if out.Records[0]["customer_id"] == nil {
		t.Fatalf("customer_id should remain present, got %+v", out.Records[0])
	}
	customer, present := out.Records[0]["customer"]
	if !present {
		t.Fatalf("customer key missing from %+v", out.Records[0])
	}
	if customer == nil {
		return nil
	}
	return customer.(map[string]any)
}

func TestHostORM_ExpandsMany2One_CrossModuleTarget(t *testing.T) {
	ctx := t.Context()
	slug := fmt.Sprintf("ormrelxmodtest%d", time.Now().UnixNano())
	primaryDB := createCrossModuleFixture(t, slug)

	contact := crossModuleContactDecl(model.Text().Required())
	mc := newCrossModuleContext(slug, &contact, false)

	t.Run("search_read", func(t *testing.T) {
		customer := searchReadCustomer(t, ctx, mc, primaryDB)
		if customer["display_name"] != "Acme Corp" {
			t.Fatalf("customer = %+v, want display_name Acme Corp", customer)
		}
		if customer["id"] != crossModuleCustomerID {
			t.Fatalf("customer.id = %v, want %s", customer["id"], crossModuleCustomerID)
		}
	})

	t.Run("read", func(t *testing.T) {
		out, hostErr := ORMRead(ctx, primaryDB, nil, mc, abiv1.ORMReadInput{Model: "sales.order", IDs: []string{crossModuleOrderID}})
		if hostErr != nil {
			t.Fatalf("ORMRead: %+v", hostErr)
		}
		customer, ok := out.Records[0]["customer"].(map[string]any)
		if !ok || customer["display_name"] != "Acme Corp" {
			t.Fatalf("customer = %+v, want display_name Acme Corp", out.Records[0]["customer"])
		}
	})
}

func TestHostORM_ExpandsMany2One_TargetModuleNotLoadedYieldsNull(t *testing.T) {
	ctx := t.Context()
	slug := fmt.Sprintf("ormrelabsentmodtest%d", time.Now().UnixNano())
	primaryDB := createCrossModuleFixture(t, slug)

	mc := newCrossModuleContext(slug, nil, false)

	if customer := searchReadCustomer(t, ctx, mc, primaryDB); customer != nil {
		t.Fatalf("customer = %+v, want nil for a target module that isn't loaded", customer)
	}

	out, hostErr := ORMRead(ctx, primaryDB, nil, mc, abiv1.ORMReadInput{Model: "sales.order", IDs: []string{crossModuleOrderID}})
	if hostErr != nil {
		t.Fatalf("ORMRead: %+v", hostErr)
	}
	if v, ok := out.Records[0]["customer"]; !ok || v != nil {
		t.Fatalf("customer = %+v (present=%v), want present and nil", v, ok)
	}
}

func TestHostORM_ExpandsMany2One_LoadedModuleMissingModelIsError(t *testing.T) {
	ctx := t.Context()
	slug := fmt.Sprintf("ormrelmissingmodeltest%d", time.Now().UnixNano())
	primaryDB := createCrossModuleFixture(t, slug)

	other := model.ModelDeclaration{Name: "other", Table: "others", Fields: []model.NamedField{
		{Name: "id", Def: model.UUID().Required().PrimaryKey()},
	}}
	mc := newCrossModuleContext(slug, &other, false)

	_, hostErr := ORMRead(ctx, primaryDB, nil, mc, abiv1.ORMReadInput{Model: "sales.order", IDs: []string{crossModuleOrderID}})
	if hostErr == nil || hostErr.Code != abiv1.ErrCodeUnavailable {
		t.Fatalf("hostErr = %+v, want %s", hostErr, abiv1.ErrCodeUnavailable)
	}
}

func TestHostORM_ExpandsMany2One_AppliesTargetFieldSecurity(t *testing.T) {
	tests := []struct {
		name        string
		displayName model.FieldDef
		granted     bool
		wantPresent bool
		wantValue   any
	}{
		{
			name:        "omit denied",
			displayName: model.Text().Required().Access(model.AccessRead(crossModuleReadPerm)).OnDeniedRead(model.Omit),
			wantPresent: false,
		},
		{
			name:        "nullify denied",
			displayName: model.Text().Required().Access(model.AccessRead(crossModuleReadPerm)).OnDeniedRead(model.Nullify),
			wantPresent: true,
			wantValue:   nil,
		},
		{
			name:        "mask denied",
			displayName: model.Text().Required().Access(model.AccessRead(crossModuleReadPerm)).OnDeniedRead(model.Mask("****{last4}")),
			wantPresent: true,
			wantValue:   "****Corp",
		},
		{
			name:        "omit granted",
			displayName: model.Text().Required().Access(model.AccessRead(crossModuleReadPerm)).OnDeniedRead(model.Omit),
			granted:     true,
			wantPresent: true,
			wantValue:   "Acme Corp",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := t.Context()
			slug := fmt.Sprintf("ormrelfsectest%d", time.Now().UnixNano())
			primaryDB := createCrossModuleFixture(t, slug)

			contact := crossModuleContactDecl(tt.displayName)
			mc := newCrossModuleContext(slug, &contact, tt.granted)

			customer := searchReadCustomer(t, ctx, mc, primaryDB)
			if customer["id"] != crossModuleCustomerID {
				t.Fatalf("customer.id = %v, want the primary key always present", customer["id"])
			}
			got, present := customer["display_name"]
			if present != tt.wantPresent || (present && got != tt.wantValue) {
				t.Fatalf("display_name = %v (present=%v), want %v (present=%v)", got, present, tt.wantValue, tt.wantPresent)
			}
		})
	}
}

func TestORMRead_SkipFieldSecurityLeavesExpandedRelationUnmasked(t *testing.T) {
	ctx := t.Context()
	slug := fmt.Sprintf("ormrelskipfsectest%d", time.Now().UnixNano())
	primaryDB := createCrossModuleFixture(t, slug)

	contact := crossModuleContactDecl(model.Text().Required().Access(model.AccessRead(crossModuleReadPerm)).OnDeniedRead(model.Omit))
	mc := newCrossModuleContext(slug, &contact, false)

	out, hostErr := ORMRead(ctx, primaryDB, nil, mc, abiv1.ORMReadInput{Model: "sales.order", IDs: []string{crossModuleOrderID}}, SkipFieldSecurity())
	if hostErr != nil {
		t.Fatalf("ORMRead: %+v", hostErr)
	}
	customer, ok := out.Records[0]["customer"].(map[string]any)
	if !ok || customer["display_name"] != "Acme Corp" {
		t.Fatalf("customer = %+v, want the unmasked display_name", out.Records[0]["customer"])
	}
}

func TestResolveRelationTarget(t *testing.T) {
	contact := crossModuleContactDecl(model.Text().Required())
	loaded := newCrossModuleContext("slug", &contact, false)
	noSnapshot := NewModuleContext("req-1", "sales", "user-1", "contact-1", nil, nil,
		"tenant-id-1", "slug", "trace-1", abi.CapDBRead, nil,
		ModuleSnapshot{ModelDecls: []model.ModelDeclaration{crossModuleOrderDecl("contacts.contact")}})

	tests := []struct {
		name       string
		mc         *ModuleContext
		related    string
		wantLoaded bool
		wantOK     bool
	}{
		{"own model", loaded, "sales.order", true, true},
		{"loaded module's model", loaded, "contacts.contact", true, true},
		{"loaded module without the model", loaded, "contacts.missing", true, false},
		{"module not loaded", loaded, "billing.invoice", false, false},
		{"name without a module", loaded, "contact", true, false},
		{"no registry snapshot", noSnapshot, "contacts.contact", true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, gotLoaded, gotOK := resolveRelationTarget(tt.mc, tt.related)
			if gotLoaded != tt.wantLoaded || gotOK != tt.wantOK {
				t.Fatalf("resolveRelationTarget(%q) = (loaded=%v, ok=%v), want (loaded=%v, ok=%v)",
					tt.related, gotLoaded, gotOK, tt.wantLoaded, tt.wantOK)
			}
		})
	}
}
