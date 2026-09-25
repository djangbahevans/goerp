package modeltable

import (
	"testing"

	"github.com/djangbahevans/goerp/sdk/go/model"
)

func TestName(t *testing.T) {
	cases := []struct {
		md   model.ModelDeclaration
		want string
	}{
		{model.ModelDeclaration{Name: "widget"}, "widget"},
		{model.ModelDeclaration{Name: "widget", Table: "custom_widgets"}, "custom_widgets"},
		{model.ModelDeclaration{Name: "SalesOrder"}, "sales_order"},
		{model.ModelDeclaration{Name: "sales.order"}, "sales_order"},
		{model.ModelDeclaration{Name: "purchaseOrderLine"}, "purchase_order_line"},
		{model.ModelDeclaration{Name: "HTTPLog"}, "httplog"},
	}
	for _, c := range cases {
		if got := Name(c.md); got != c.want {
			t.Errorf("Name(%+v) = %q, want %q", c.md, got, c.want)
		}
	}
}
