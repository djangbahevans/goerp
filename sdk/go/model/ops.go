package model

// Op represents one of EnableOps' six reserved CRUD/list operations,
// optionally carrying a per-op ABAC domain condition. Mirrors
// engine.ActionName's reserved names (sdk/go/engine) — kept as its own
// type here rather than reused from there, since engine imports model,
// not the other way around.
type Op struct {
	Name      string `msgpack:"name"`
	Condition string `msgpack:"condition,omitempty"`
}

// WithCondition attaches a per-op ABAC domain condition — the same
// expression grammar Many2One's .Domain() and RLS policies compile
// (internal/engine/domain) — evaluated server-side against the fetched
// record for storage backends with no table to compile a policy against
// (Transient, Virtual), rather than compiled to SQL.
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
)
