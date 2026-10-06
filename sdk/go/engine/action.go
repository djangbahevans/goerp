package engine

import (
	"fmt"
	"reflect"

	"github.com/djangbahevans/goerp/sdk/go/model"
)

// CRUDAction names the reserved operation an engine.Model route serves.
type CRUDAction string

// Reserved names EnableOps registers automatically; a definition with one
// of these names overrides the auto-generated version for that name only.
// They are untyped so each converts to CRUDAction without a cast.
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

// NoBody is the Req of an action with no request body. Its handler ignores
// the parameter and the body is not decoded.
type NoBody struct{}

// ActionDef binds a model type, an action name, a request body type and the
// action's options. Register its handler with HandleAction.
type ActionDef[M model.Named, Req any] struct {
	name string
	opts []ActionOption
}

// DefineAction declares a named action on model M. The route is identified
// by (M's resource name, name); the engine derives its method and path from
// the model's declaration the same way EnableOps does for the seven reserved
// names (go-sdk-reference.md §2a "Path derivation"). Req is the JSON request
// body type, which also types the action in goerp codegen.
func DefineAction[M model.Named, Req any](name string, opts ...ActionOption) ActionDef[M, Req] {
	return ActionDef[M, Req]{name: name, opts: opts}
}

// HandleAction registers handler for def, called in init(). The request body
// is decoded into Req before handler runs; a body that does not decode
// answers 400 without calling it. Registering the same model and action name
// twice fails the module's load in the engine.
func HandleAction[M model.Named, Req any](def ActionDef[M, Req], handler func(*Request, Req) *Response) {
	if handler == nil {
		panic(fmt.Sprintf("engine.HandleAction: action %q has a nil handler", def.name))
	}
	var m M
	hasBody := reflect.TypeFor[Req]() != reflect.TypeFor[NoBody]()
	var requestType *TypeDesc
	if hasBody {
		requestType = new(describeType(reflect.TypeFor[Req]()))
	}
	DefaultRouter.registerAction(m.ResourceName(), def.name, requestType, func(req *Request) *Response {
		var body Req
		if hasBody {
			if err := req.ParseJSON(&body); err != nil {
				return BadRequest(err)
			}
		}
		return handler(req, body)
	}, def.opts...)
}

func crudActionOf(name string) string {
	switch name {
	case List, Get, Create, Update, Delete, Preview, Pivot:
		return name
	default:
		return ""
	}
}
