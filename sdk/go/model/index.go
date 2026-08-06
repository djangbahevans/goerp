package model

type IndexKind int

const (
	KindBTree IndexKind = iota
	KindGIN
)

type IndexDef struct {
	Kind      IndexKind `msgpack:"kind"`
	Columns   []string  `msgpack:"columns"`
	IsUnique  bool      `msgpack:"unique,omitempty"`
	WhereExpr string    `msgpack:"where,omitempty"`
	Ops       string    `msgpack:"ops,omitempty"` // GIN only, e.g. "gin_trgm_ops"
}

func BTreeIndex(columns ...string) IndexDef {
	return IndexDef{Kind: KindBTree, Columns: columns}
}

func GINIndex(column string) IndexDef {
	return IndexDef{Kind: KindGIN, Columns: []string{column}}
}

func (i IndexDef) Unique() IndexDef            { i.IsUnique = true; return i }
func (i IndexDef) Where(condition string) IndexDef { i.WhereExpr = condition; return i }
func (i IndexDef) WithOps(ops string) IndexDef { i.Ops = ops; return i }
