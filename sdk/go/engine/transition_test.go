package engine

import (
	"testing"

	"github.com/djangbahevans/goerp/sdk/go/model"
	"github.com/djangbahevans/goerp/sdk/go/perm"
)

func TestHandleTransition_DeclaresOverrideAndDispatchesNoBody(t *testing.T) {
	router := withRouter(t)
	transition := model.Transition("draft", "confirmed", "confirm").Requires(perm.Ref("sales:order:confirm"))
	called := false
	HandleTransition[testOrder](transition, func(req *Request, _ NoBody) *Response {
		called = req.PathParams["id"] == "order-1"
		return &Response{StatusCode: 202}
	})
	HandleAction(DefineAction[testOrder, NoBody]("archive"), func(*Request, NoBody) *Response { return nil })

	decls := routeDeclarations(router.routes)
	got := decls[0]
	if got.Model != "sales.order" || got.Name != "confirm" || got.Transition == nil ||
		got.Transition.From != "draft" || got.Transition.To != "confirmed" || got.RequestType != nil {
		t.Fatalf("transition declaration = %+v", got)
	}
	if got.Method != "" || got.Path != "" || got.Scope != "" || decls[1].Transition != nil {
		t.Fatalf("route identities = %+v", decls)
	}

	response := router.Handle(&Request{
		Model:      "sales.order",
		Action:     "confirm",
		PathParams: map[string]string{"id": "order-1"},
		Body:       []byte("invalid JSON"),
	})
	if !called || response.StatusCode != 202 {
		t.Fatalf("called=%v response=%+v", called, response)
	}
}

func TestHandleTransition_NilHandlerDoesNotRegister(t *testing.T) {
	router := withRouter(t)
	defer func() {
		if recover() == nil || len(router.routes) != 0 {
			t.Fatal("nil transition handler must panic without registering")
		}
	}()

	HandleTransition[testOrder](model.Transition("draft", "confirmed", "confirm"), nil)
}
