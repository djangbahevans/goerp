package model

import "github.com/djangbahevans/goerp/sdk/go/perm"

// Op declares an EnableOps operation with an optional permission and ABAC condition.
// An operation without a permission requires authentication only.
type Op struct {
	Name       string `msgpack:"name"`
	Permission string `msgpack:"permission,omitempty"`
	Condition  string `msgpack:"condition,omitempty"`
}

// Requires sets the permission required to invoke the generated route.
// It panics on a zero perm.Permission. Field access rules remain independent.
func (o Op) Requires(permission perm.Permission) Op {
	if permission.Name() == "" {
		panic("model.Op.Requires: zero perm.Permission")
	}

	o.Permission = permission.Name()

	return o
}

// WithCondition attaches a per-op ABAC domain condition, in the same
// expression language as Many2One's .Domain(). For Transient and Virtual
// models, which have no table, the engine evaluates it against each
// fetched record instead of compiling it to SQL.
func (o Op) WithCondition(expr string) Op {
	o.Condition = expr
	return o
}

var (
	List    = Op{Name: "list"}
	Get     = Op{Name: "get"}
	Create  = Op{Name: "create"}
	Update  = Op{Name: "update"}
	Delete  = Op{Name: "delete"}
	Preview = Op{Name: "preview"}
	Pivot   = Op{Name: "pivot"}
)
