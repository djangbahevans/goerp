package engine

// ActionName is a string type — the same string a view JSON's "route"
// field or useAction(...) references (go-sdk-reference.md §2a).
type ActionName string

// Reserved names EnableOps registers automatically; engine.Action with one
// of these overrides the auto-generated version for that name only.
const (
	List    ActionName = "list"
	Get     ActionName = "get"
	Create  ActionName = "create"
	Update  ActionName = "update"
	Delete  ActionName = "delete"
	Preview ActionName = "preview"
	Pivot   ActionName = "pivot"
)

// Action registers a named action on a model. The route is identified by
// (model, name); the engine derives its method and path from the model's
// declaration the same way EnableOps does for the seven reserved names
// (go-sdk-reference.md §2a "Path derivation").
func Action(model string, name ActionName, handler Handler) {
	DefaultRouter.registerAction(model, string(name), crudActionOf(name), handler)
}

func crudActionOf(name ActionName) string {
	switch name {
	case List, Get, Create, Update, Delete, Preview, Pivot:
		return string(name)
	default:
		return ""
	}
}
