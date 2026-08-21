package model

type FieldKind int

const (
	KindChar FieldKind = iota
	KindText
	KindInteger
	KindBigInt
	KindFloat
	KindDecimal
	KindBoolean
	KindUUID
	KindTimestampTZ
	KindDate
	KindTime
	KindJSONB
	KindBytea
	KindSelection
	KindEnum
)

type FieldDef struct {
	Kind            FieldKind `msgpack:"kind"`
	Length          int       `msgpack:"length,omitempty"`    // Char(n); 0 = unbounded TEXT
	Precision       int       `msgpack:"precision,omitempty"` // Decimal(p, s)
	Scale           int       `msgpack:"scale,omitempty"`
	SelectionValues []string  `msgpack:"selection_values,omitempty"`
	EnumType        string    `msgpack:"enum_type,omitempty"`
	IsRequired      bool      `msgpack:"required,omitempty"`
	IsPrimaryKey    bool      `msgpack:"primary_key,omitempty"`
	DefaultExpr     *string   `msgpack:"default_expr,omitempty"`
}

// Char(n) sets VARCHAR(n); called with no argument it's an unbounded TEXT column.
func Char(n ...int) FieldDef {
	length := 0
	if len(n) > 0 {
		length = n[0]
	}
	return FieldDef{Kind: KindChar, Length: length}
}

func Text() FieldDef    { return FieldDef{Kind: KindText} }
func Integer() FieldDef { return FieldDef{Kind: KindInteger} }
func BigInt() FieldDef  { return FieldDef{Kind: KindBigInt} }
func Float() FieldDef   { return FieldDef{Kind: KindFloat} }
func Decimal(precision, scale int) FieldDef {
	return FieldDef{Kind: KindDecimal, Precision: precision, Scale: scale}
}
func Boolean() FieldDef     { return FieldDef{Kind: KindBoolean} }
func UUID() FieldDef        { return FieldDef{Kind: KindUUID} }
func TimestampTZ() FieldDef { return FieldDef{Kind: KindTimestampTZ} }
func Date() FieldDef        { return FieldDef{Kind: KindDate} }
func Time() FieldDef        { return FieldDef{Kind: KindTime} }
func JSONB() FieldDef       { return FieldDef{Kind: KindJSONB} }
func Bytea() FieldDef       { return FieldDef{Kind: KindBytea} }
func Selection(values ...string) FieldDef {
	return FieldDef{Kind: KindSelection, SelectionValues: values}
}
func Enum(typeName string) FieldDef { return FieldDef{Kind: KindEnum, EnumType: typeName} }

func (f FieldDef) Required() FieldDef           { f.IsRequired = true; return f }
func (f FieldDef) PrimaryKey() FieldDef         { f.IsPrimaryKey = true; return f }
func (f FieldDef) Default(expr string) FieldDef { f.DefaultExpr = &expr; return f }
