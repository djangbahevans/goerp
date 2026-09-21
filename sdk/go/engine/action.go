package engine

// ActionName is a string type — the same string a view JSON's "route"
// field or useAction(...) references (go-sdk-reference.md §2a).
type ActionName string

// CRUDAction names the reserved operation an engine.Model route serves.
type CRUDAction string

// Reserved names EnableOps registers automatically; engine.Action with one
// of these overrides the auto-generated version for that name only. They
// are untyped so each converts to ActionName and CRUDAction without a cast.
const (
	List    = "list"
	Get     = "get"
	Create  = "create"
	Update  = "update"
	Delete  = "delete"
	Preview = "preview"
	Pivot   = "pivot"
)

// HTTPMethod is the HTTP method of a custom action (engine.Method).
type HTTPMethod string

const (
	MethodGet    HTTPMethod = "GET"
	MethodPost   HTTPMethod = "POST"
	MethodPut    HTTPMethod = "PUT"
	MethodPatch  HTTPMethod = "PATCH"
	MethodDelete HTTPMethod = "DELETE"
)

// ActionScope selects whether a custom action addresses one record or the
// collection (engine.Scope).
type ActionScope string

const (
	RecordAction     ActionScope = "record"
	CollectionAction ActionScope = "collection"
)

type actionConfig struct {
	routeConfig
	method HTTPMethod
	scope  ActionScope
}

type actionOptionFunc func(*actionConfig)

func (f actionOptionFunc) applyAction(c *actionConfig) { f(c) }

// Method sets the HTTP method of a custom action. Default: POST. The
// method of a reserved name is fixed.
func Method(m HTTPMethod) ActionOption {
	return actionOptionFunc(func(c *actionConfig) { c.method = m })
}

// Scope sets whether a custom action addresses one record (the default,
// /{plural}/{id}/{name}) or the collection (/{plural}/{name}).
func Scope(s ActionScope) ActionOption {
	return actionOptionFunc(func(c *actionConfig) { c.scope = s })
}

// Action registers a named action on a model. The route is identified by
// (model, name); the engine derives its method and path from the model's
// declaration the same way EnableOps does for the seven reserved names
// (go-sdk-reference.md §2a "Path derivation").
func Action(model string, name ActionName, handler Handler, opts ...ActionOption) {
	DefaultRouter.registerAction(model, string(name), handler, opts...)
}

func crudActionOf(name ActionName) string {
	switch name {
	case List, Get, Create, Update, Delete, Preview, Pivot:
		return string(name)
	default:
		return ""
	}
}
