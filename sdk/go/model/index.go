package model

type IndexKind int

const (
	KindBTree IndexKind = iota
	KindGIN
	KindPartial
	KindBRIN
	KindGiST
)

type IndexDef struct {
	Kind      IndexKind `msgpack:"kind"`
	Columns   []string  `msgpack:"columns"`
	IsUnique  bool      `msgpack:"unique,omitempty"`
	WhereExpr string    `msgpack:"where,omitempty"`
	Ops       string    `msgpack:"ops,omitempty"`             // GIN only, e.g. "gin_trgm_ops"
	Pages     int       `msgpack:"pages_per_range,omitempty"` // BRIN only
}

func BTreeIndex(columns ...string) IndexDef {
	return IndexDef{Kind: KindBTree, Columns: columns}
}

func GINIndex(column string) IndexDef {
	return IndexDef{Kind: KindGIN, Columns: []string{column}}
}

func PartialIndex(columns ...string) IndexDef {
	return IndexDef{Kind: KindPartial, Columns: columns}
}

func BRINIndex(column string) IndexDef {
	return IndexDef{Kind: KindBRIN, Columns: []string{column}}
}

func GiSTIndex(column string) IndexDef {
	return IndexDef{Kind: KindGiST, Columns: []string{column}}
}

func (i IndexDef) Unique() IndexDef                { i.IsUnique = true; return i }
func (i IndexDef) Where(condition string) IndexDef { i.WhereExpr = condition; return i }
func (i IndexDef) WithOps(ops string) IndexDef     { i.Ops = ops; return i }
func (i IndexDef) PagesPerRange(n int) IndexDef    { i.Pages = n; return i }
