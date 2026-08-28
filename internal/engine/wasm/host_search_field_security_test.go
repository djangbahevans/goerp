package wasm

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/djangbahevans/goerp/internal/engine/fieldsec"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/permission"
	"github.com/djangbahevans/goerp/internal/engine/searchindex"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

// newSearchFieldSecModuleContext is newFieldSecModuleContext
// (host_orm_field_security_test.go) plus a SearchIndexRegistry declaring
// "widgets" over the same "testmodule.widget" model/table that helper's
// own FieldSecurityRegistry/table fixture already use — so
// host.search.query's field masking can be exercised against the exact
// same rules host.orm.read's own field-security tests already cover.
func newSearchFieldSecModuleContext(slug string, grantedPermissions ...string) *ModuleContext {
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
			panic("newSearchFieldSecModuleContext: unregistered permission " + name)
		}
		permSet.Set(idx)
	}

	searchIndexReg := searchindex.New()
	searchIndexReg.Register("testmodule", []manifest.SearchIndex{
		{
			Name:       "widgets",
			Resource:   "testmodule.widget",
			Table:      "widgets",
			Searchable: []string{"name"},
			Displayed:  []string{"id", "name", "credit_limit", "bank_account", "notes"},
		},
	})

	return NewModuleContext("req-1", "testmodule", "user-1", "contact-1", []string{"admin"}, permSet,
		"tenant-id-1", slug, "trace-1", abi.CapSearchQuery, nil,
		ModuleSnapshot{
			FieldSecRegistry:    fieldSecReg,
			PermissionRegistry:  permReg,
			SearchIndexRegistry: searchIndexReg,
		})
}

func TestSearchQuery_FieldSecurity_DeniedFieldsMaskedPerBehaviour(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("searchfieldsectest%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	id := "11111111-1111-1111-1111-111111111111"
	createFieldSecFixtureTable(t, primaryDB, slug, id)

	// No permissions granted — every restricted field should be denied.
	modCtx := newSearchFieldSecModuleContext(slug)

	out, hostErr := SearchQuery(ctx, primaryDB, modCtx, SearchQueryInput{Index: "widgets", Query: "Widget"})
	if hostErr != nil {
		t.Fatalf("SearchQuery: %+v", hostErr)
	}
	if len(out.Hits) != 1 {
		t.Fatalf("len(Hits) = %d, want 1", len(out.Hits))
	}
	hit := out.Hits[0]

	if _, ok := hit["credit_limit"]; ok {
		t.Errorf("credit_limit should be omitted (OnDeniedRead: Omit), got %+v", hit)
	}
	if got := hit["bank_account"]; got != "****7890" {
		t.Errorf("bank_account = %v, want masked \"****7890\" (OnDeniedRead: Mask)", got)
	}
	if got, ok := hit["notes"]; !ok || got != nil {
		t.Errorf("notes = %v (present=%v), want present and nil (OnDeniedRead: Nullify)", got, ok)
	}
	if hit["name"] != "Widget A" {
		t.Errorf("name = %v, want unrestricted \"Widget A\" unchanged", hit["name"])
	}
}

func TestSearchQuery_FieldSecurity_GrantedPermissionAllowsField(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("searchfieldsectest%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	id := "11111111-1111-1111-1111-111111111111"
	createFieldSecFixtureTable(t, primaryDB, slug, id)

	modCtx := newSearchFieldSecModuleContext(slug, "contacts:contact:financials_read")

	out, hostErr := SearchQuery(ctx, primaryDB, modCtx, SearchQueryInput{Index: "widgets", Query: "Widget"})
	if hostErr != nil {
		t.Fatalf("SearchQuery: %+v", hostErr)
	}
	if len(out.Hits) != 1 {
		t.Fatalf("len(Hits) = %d, want 1", len(out.Hits))
	}
	if got := out.Hits[0]["credit_limit"]; fmt.Sprint(got) != "5000" {
		t.Errorf("credit_limit = %v, want 5000 (caller holds financials_read)", got)
	}
}
