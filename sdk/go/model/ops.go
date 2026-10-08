package model

// Op represents one of EnableOps' seven reserved CRUD/list operations,
// optionally carrying a per-op ABAC domain condition. Mirrors
// engine's reserved action names (sdk/go/engine) — kept as its own
// type here rather than reused from there, since engine imports model,
// not the other way around.
type Op struct {
	Name      string `msgpack:"name"`
	Condition string `msgpack:"condition,omitempty"`
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
