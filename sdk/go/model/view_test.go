package model

import (
	"testing"

	"github.com/vmihailenco/msgpack/v5"
)

func TestViewType_ReservedNames(t *testing.T) {
	if ListView.Name != "list" {
		t.Errorf("ListView.Name = %q, want %q", ListView.Name, "list")
	}
	if FormView.Name != "form" {
		t.Errorf("FormView.Name = %q, want %q", FormView.Name, "form")
	}
}

func TestModelDeclaration_EnableViews_Appends(t *testing.T) {
	md := Define("widget").EnableViews(ListView, FormView)
	if len(md.EnabledViews) != 2 || md.EnabledViews[0] != ListView || md.EnabledViews[1] != FormView {
		t.Fatalf("EnabledViews = %v, want [ListView FormView]", md.EnabledViews)
	}
}

func TestModelDeclaration_Nav_SetsDeclaration(t *testing.T) {
	md := Define("widget").Nav("Sales", "Orders", 20)
	if md.NavDecl == nil {
		t.Fatal("NavDecl = nil, want a declaration")
	}
	want := NavDeclaration{Group: "Sales", Label: "Orders", Order: 20}
	if *md.NavDecl != want {
		t.Fatalf("NavDecl = %+v, want %+v", *md.NavDecl, want)
	}
}

func TestModelDeclaration_Nav_OverwritesPriorCall(t *testing.T) {
	md := Define("widget").Nav("Sales", "Orders", 20).Nav("Sales", "Invoices", 30)
	want := NavDeclaration{Group: "Sales", Label: "Invoices", Order: 30}
	if *md.NavDecl != want {
		t.Fatalf("NavDecl = %+v, want %+v (second call wins)", *md.NavDecl, want)
	}
}

func TestModelDeclaration_EnableViewsAndNav_MsgpackRoundTrip(t *testing.T) {
	md := Define("widget").
		EnableOps(List, Get, Create, Update).
		EnableViews(ListView, FormView).
		Nav("Sales", "Orders", 20)

	data, err := msgpack.Marshal(md)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var got ModelDeclaration
	if err := msgpack.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if len(got.EnabledViews) != 2 || got.EnabledViews[0] != ListView || got.EnabledViews[1] != FormView {
		t.Fatalf("round-tripped EnabledViews = %v, want [ListView FormView]", got.EnabledViews)
	}
	if got.NavDecl == nil || *got.NavDecl != *md.NavDecl {
		t.Fatalf("round-tripped NavDecl = %v, want %+v", got.NavDecl, *md.NavDecl)
	}
}

func TestFieldDef_Readonly(t *testing.T) {
	f := Char().Readonly()
	if !f.IsReadonly {
		t.Fatal("IsReadonly = false, want true after .Readonly()")
	}
	if Char().IsReadonly {
		t.Fatal("Char() without .Readonly() should default to false")
	}
}
