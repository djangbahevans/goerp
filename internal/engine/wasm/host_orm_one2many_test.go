package wasm

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

// widgetModelDeclWithOne2Many is widgetModelDecl plus a virtual One2Many
// field with no backing column, used to test that host.orm's read/write
// paths never treat it as a real column.
func widgetModelDeclWithOne2Many() model.ModelDeclaration {
	md := widgetModelDecl()
	md.Fields = append(md.Fields, model.NamedField{
		Name: "tag_ids",
		Def:  model.One2Many("testmodule.tag", "widget_id"),
	})
	return md
}

func TestHostORM_SearchRead_One2Many_ExcludedFromDefaultFields(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("ormone2manydefault%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)

	id1 := "11111111-1111-1111-1111-111111111111"
	createFixtureWidgetsTable(t, primaryDB, slug, [][2]string{{id1, "Widget A"}})

	r := newHostDBTestRuntime(t, primaryDB, 10)
	mc := newORMTestModuleContext(slug, []model.ModelDeclaration{widgetModelDeclWithOne2Many()})
	inst := newHostORMCaller(t, ctx, r, mc)

	var out ORMSearchReadOutput
	env := callORMHost(t, ctx, inst, "call_search_read", ORMSearchReadInput{Model: "testmodule.widget"}, &out)
	if !env.OK {
		t.Fatalf("search_read failed: %+v", env.Error)
	}
	rec := out.Records[0]
	if _, ok := rec["tag_ids"]; ok {
		t.Fatalf("expected tag_ids (One2Many) to be excluded from the default field list, got %+v", rec)
	}
}

func TestHostORM_SearchRead_One2Many_ExplicitRequestRejected(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("ormone2manyrequest%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	createFixtureWidgetsTable(t, primaryDB, slug, nil)

	r := newHostDBTestRuntime(t, primaryDB, 10)
	mc := newORMTestModuleContext(slug, []model.ModelDeclaration{widgetModelDeclWithOne2Many()})
	inst := newHostORMCaller(t, ctx, r, mc)

	env := callORMHost(t, ctx, inst, "call_search_read", ORMSearchReadInput{Model: "testmodule.widget", Fields: []string{"tag_ids"}}, nil)
	if env.OK {
		t.Fatal("expected an error when explicitly requesting a One2Many field")
	}
	if env.Error.Code != abi.ErrCodeFieldUnknown {
		t.Fatalf("error code = %q, want %q", env.Error.Code, abi.ErrCodeFieldUnknown)
	}
}

func TestORMCreate_One2Many_RejectedAsFieldNotWritable(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("ormone2manywrite%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	createFixtureWidgetsTable(t, primaryDB, slug, nil)

	r := newHostDBTestRuntime(t, primaryDB, 10)
	decls := []model.ModelDeclaration{widgetModelDeclWithOne2Many()}
	mc := NewModuleContext("req-1", "testmodule", "user-1", "contact-1", []string{"admin"}, nil, slug, slug, "trace-1",
		abi.CapDBRead|abi.CapDBWrite, nil, ModuleSnapshot{ModelDecls: decls})

	_, hostErr := ORMCreate(ctx, r, primaryDB, r.EventInsertClient(), nil, mc, ORMCreateInput{
		Model: "testmodule.widget",
		Record: map[string]any{
			"id": "22222222-2222-2222-2222-222222222222", "name": "Widget B",
			"tag_ids": []string{"33333333-3333-3333-3333-333333333333"},
		},
	})
	if hostErr == nil || hostErr.Code != abi.ErrCodeFieldNotWritable {
		t.Fatalf("ORMCreate with a One2Many field in payload: hostErr = %+v, want code %s", hostErr, abi.ErrCodeFieldNotWritable)
	}
}
