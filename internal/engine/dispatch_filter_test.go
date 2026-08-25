package engine

import (
	"net/url"
	"testing"

	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/djangbahevans/goerp/internal/engine/domain"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

func widgetFilterTestModel() model.ModelDeclaration {
	return model.ModelDeclaration{
		Name: "widget",
		Fields: []model.NamedField{
			{Name: "state"},
			{Name: "created_at"},
			{Name: "amount"},
		},
	}
}

// compilesToSQL proves expr is actually valid domain-expression source —
// the real internal/engine/domain.Parse/CompileToSQL pipeline
// compileListFilter's output is fed into, not just a string this test
// asserts by inspection.
func compilesToSQL(t *testing.T, expr string) (string, []any) {
	t.Helper()
	parsed, err := domain.Parse(expr)
	if err != nil {
		t.Fatalf("domain.Parse(%q) error: %v", expr, err)
	}
	frag, args, err := domain.CompileToSQL(parsed)
	if err != nil {
		t.Fatalf("domain.CompileToSQL(%q) error: %v", expr, err)
	}
	return frag, args
}

func TestCompileListFilter_NoParamsProducesUnfilteredDomain(t *testing.T) {
	expr, hostErr := compileListFilter(url.Values{}, "testmodule.widget", widgetFilterTestModel())
	if hostErr != nil {
		t.Fatalf("compileListFilter error: %v", hostErr)
	}
	if expr != "true" {
		t.Errorf("expr = %q, want %q", expr, "true")
	}
}

func TestCompileListFilter_ImplicitEqualityMatchesDocumentedExample(t *testing.T) {
	q, _ := url.ParseQuery("filter[state]=confirmed")
	expr, hostErr := compileListFilter(q, "testmodule.widget", widgetFilterTestModel())
	if hostErr != nil {
		t.Fatalf("compileListFilter error: %v", hostErr)
	}
	want := "record.state = 'confirmed'"
	if expr != want {
		t.Errorf("expr = %q, want %q", expr, want)
	}

	frag, args := compilesToSQL(t, expr)
	if frag != `("state" = $1)` {
		t.Errorf("frag = %s, want (\"state\" = $1)", frag)
	}
	if len(args) != 1 || args[0] != "confirmed" {
		t.Errorf("args = %#v, want [confirmed]", args)
	}
}

func TestCompileListFilter_GteOperatorMatchesDocumentedExample(t *testing.T) {
	q, _ := url.ParseQuery("filter[created_at][gte]=2026-01-01")
	expr, hostErr := compileListFilter(q, "testmodule.widget", widgetFilterTestModel())
	if hostErr != nil {
		t.Fatalf("compileListFilter error: %v", hostErr)
	}
	want := "record.created_at >= '2026-01-01'"
	if expr != want {
		t.Errorf("expr = %q, want %q", expr, want)
	}
	compilesToSQL(t, expr)
}

func TestCompileListFilter_MultipleParamsCombineWithAND(t *testing.T) {
	q, _ := url.ParseQuery("filter[state]=confirmed&filter[amount][gt]=100")
	expr, hostErr := compileListFilter(q, "testmodule.widget", widgetFilterTestModel())
	if hostErr != nil {
		t.Fatalf("compileListFilter error: %v", hostErr)
	}
	want := "record.amount > '100' AND record.state = 'confirmed'"
	if expr != want {
		t.Errorf("expr = %q, want %q", expr, want)
	}
	compilesToSQL(t, expr)
}

func TestCompileListFilter_InOperatorProducesInExpr(t *testing.T) {
	q, _ := url.ParseQuery("filter[state][in]=draft,confirmed")
	expr, hostErr := compileListFilter(q, "testmodule.widget", widgetFilterTestModel())
	if hostErr != nil {
		t.Fatalf("compileListFilter error: %v", hostErr)
	}
	want := "record.state IN ('draft', 'confirmed')"
	if expr != want {
		t.Errorf("expr = %q, want %q", expr, want)
	}
	frag, args := compilesToSQL(t, expr)
	if frag != `("state" IN ($1, $2))` {
		t.Errorf("frag = %s, want (\"state\" IN ($1, $2))", frag)
	}
	if len(args) != 2 || args[0] != "draft" || args[1] != "confirmed" {
		t.Errorf("args = %#v, want [draft confirmed]", args)
	}
}

func TestCompileListFilter_UndeclaredFieldReturnsFieldUnknown(t *testing.T) {
	q, _ := url.ParseQuery("filter[nonexistent]=x")
	_, hostErr := compileListFilter(q, "testmodule.widget", widgetFilterTestModel())
	if hostErr == nil {
		t.Fatal("expected an error for an undeclared filter field")
	}
	if hostErr.Code != abi.ErrCodeFieldUnknown {
		t.Errorf("code = %q, want %q", hostErr.Code, abi.ErrCodeFieldUnknown)
	}
}

func TestCompileListFilter_UnknownOperatorReturnsDomainInvalid(t *testing.T) {
	q, _ := url.ParseQuery("filter[state][bogus]=x")
	_, hostErr := compileListFilter(q, "testmodule.widget", widgetFilterTestModel())
	if hostErr == nil {
		t.Fatal("expected an error for an unknown filter operator")
	}
	if hostErr.Code != abi.ErrCodeDomainInvalid {
		t.Errorf("code = %q, want %q", hostErr.Code, abi.ErrCodeDomainInvalid)
	}
}

func TestCompileListFilter_ValueWithSingleQuoteIsEscaped(t *testing.T) {
	q, _ := url.ParseQuery("filter[state]=" + url.QueryEscape("O'Brien"))
	expr, hostErr := compileListFilter(q, "testmodule.widget", widgetFilterTestModel())
	if hostErr != nil {
		t.Fatalf("compileListFilter error: %v", hostErr)
	}
	want := "record.state = 'O''Brien'"
	if expr != want {
		t.Errorf("expr = %q, want %q", expr, want)
	}

	frag, args := compilesToSQL(t, expr)
	if frag != `("state" = $1)` {
		t.Errorf("frag = %s, want (\"state\" = $1)", frag)
	}
	if len(args) != 1 || args[0] != "O'Brien" {
		t.Errorf("args = %#v, want [O'Brien] (unescaped once parsed back out)", args)
	}
}

func TestCompileListFilter_EmptyValueIsIgnoredNotAnEmptyStringMatch(t *testing.T) {
	q, _ := url.ParseQuery("filter[state]=")
	expr, hostErr := compileListFilter(q, "testmodule.widget", widgetFilterTestModel())
	if hostErr != nil {
		t.Fatalf("compileListFilter error: %v", hostErr)
	}
	if expr != "true" {
		t.Errorf("expr = %q, want %q (an empty filter value should be ignored, not compiled to an empty-string match)", expr, "true")
	}
}
