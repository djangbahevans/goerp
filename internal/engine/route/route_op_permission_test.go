package route

import (
	"slices"
	"testing"

	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/sdk/go/model"
	"github.com/djangbahevans/goerp/sdk/go/perm"
)

func TestRegisterModelRoutesOperationPermissions(t *testing.T) {
	read := perm.Ref("sales:order:read")
	write := perm.Ref("sales:order:write")
	remove := perm.Ref("sales:order:delete")
	md := model.Define("order").EnableOps(
		model.List.Requires(read),
		model.Get.Requires(read),
		model.Create.Requires(write),
		model.Update.Requires(write),
		model.Delete.Requires(remove),
		model.Preview.Requires(write),
		model.Pivot.Requires(read),
	)
	table := New()
	if _, err := RegisterModelRoutes(table, "sales", "standard", []model.ModelDeclaration{*md}); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		method     string
		path       string
		permission string
	}{
		{"GET", "/sales/orders", read.Name()},
		{"GET", "/sales/orders/123", read.Name()},
		{"POST", "/sales/orders", write.Name()},
		{"PUT", "/sales/orders/123", write.Name()},
		{"DELETE", "/sales/orders/123", remove.Name()},
		{"POST", "/sales/orders/preview", write.Name()},
		{"GET", "/sales/orders/pivot", read.Name()},
	} {
		entry, _, result, _ := table.Lookup(tc.method, tc.path)
		if result != RouteFound {
			t.Fatalf("%s %s not found", tc.method, tc.path)
		}

		if entry.Manifest.Auth != "required" || !slices.Equal(entry.Manifest.Permissions, []string{tc.permission}) {
			t.Errorf("%s %s auth = %q, permissions = %v", tc.method, tc.path, entry.Manifest.Auth, entry.Manifest.Permissions)
		}
	}
}

func TestSynthesizeViewsOperationPermissions(t *testing.T) {
	for _, restricted := range []bool{false, true} {
		list, get, create := model.List, model.Get, model.Create
		if restricted {
			list = list.Requires(perm.Ref("sales:order:list"))
			get = get.Requires(perm.Ref("sales:order:get"))
			create = create.Requires(perm.Ref("sales:order:create"))
		}

		md := model.Define("order").EnableOps(list, get, create, model.Update).
			EnableViews(model.ListView, model.FormView).Nav("Sales", "Orders", 10)
		views, _, nav, err := SynthesizeViews("sales", "standard", []model.ModelDeclaration{*md}, nil, nil)
		if err != nil {
			t.Fatal(err)
		}

		if len(views) != 2 || len(nav) != 1 || len(nav[0].Children) != 1 {
			t.Fatalf("views = %+v, navigation = %+v", views, nav)
		}
		if views[0].Permission != list.Permission || views[1].Permission != get.Permission || nav[0].Children[0].Permission != list.Permission {
			t.Errorf("view or navigation permissions do not match backing ops: views = %+v, navigation = %+v", views, nav)
		}
		if len(views[0].Actions) != 1 || views[0].Actions[0].Permission != create.Permission {
			t.Errorf("create actions = %+v", views[0].Actions)
		}

		explicit := []manifest.View{{Name: "custom_orders", Type: "list", Resource: "sales.order", Permission: "sales:order:custom"}}
		_, _, nav, err = SynthesizeViews("sales", "standard", []model.ModelDeclaration{*md}, explicit, nil)
		if err != nil {
			t.Fatal(err)
		}

		item := nav[0].Children[0]
		if item.View != "custom_orders" || item.Permission != list.Permission {
			t.Errorf("navigation with an explicit view = %+v", item)
		}
	}
}
