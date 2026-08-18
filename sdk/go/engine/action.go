package engine

import (
	"strings"

	"github.com/go-openapi/inflect"
)

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
)

// Action registers a named action on a model. The wire path is never
// author-specified — it's derived from (model, name) the same way
// EnableOps derives paths for the six reserved names (go-sdk-reference.md
// §2a "Path derivation").
func Action(model string, name ActionName, handler Handler) {
	method, path := actionPath(model, name)
	DefaultRouter.registerAction(method, path, model, string(name), crudActionOf(name), handler)
}

func actionPath(model string, name ActionName) (method, path string) {
	plural := pluralSegment(model)

	switch name {
	case List:
		return "GET", "/" + plural
	case Get:
		return "GET", "/" + plural + "/{id}"
	case Create:
		return "POST", "/" + plural
	case Update:
		return "PUT", "/" + plural + "/{id}"
	case Delete:
		return "DELETE", "/" + plural + "/{id}"
	case Preview:
		return "POST", "/" + plural + "/preview"
	default:
		// Any other name: a custom, record-scoped action (§2a's default
		// scope) — POST /{plural}/{id}/{name}.
		return "POST", "/" + plural + "/{id}/" + string(name)
	}
}

func crudActionOf(name ActionName) string {
	switch name {
	case List, Get, Create, Update, Delete, Preview:
		return string(name)
	default:
		return ""
	}
}

// pluralSegment derives a route's resource segment from a dotted model
// name ("sales.order" -> "order" -> "orders"). go-sdk-reference.md §22
// documents the real rule as pluralizing a model's LabelPlural (or the
// model name itself if LabelPlural isn't set) — LabelPlural isn't
// reachable here (no model registry exists yet to look it up from just a
// model name string), so this always takes the documented fallback path.
func pluralSegment(model string) string {
	segment := model
	if i := strings.LastIndex(model, "."); i >= 0 {
		segment = model[i+1:]
	}
	return inflect.Pluralize(segment)
}
