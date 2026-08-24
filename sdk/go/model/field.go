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
	KindMany2One
	KindSequence
)

// OnDeleteBehaviour is a Many2One field's FOREIGN KEY ON DELETE action.
// The zero value (Restrict) is the safe default for a field with no
// explicit .OnDelete() call.
type OnDeleteBehaviour int

const (
	Restrict OnDeleteBehaviour = iota
	SetNull
	Cascade
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
	IsPrimary       bool      `msgpack:"primary,omitempty"` // the model's display/label field, distinct from IsPrimaryKey
	DefaultExpr     *string   `msgpack:"default_expr,omitempty"`

	// Many2One (KindMany2One only)
	RelatedModel     string            `msgpack:"related_model,omitempty"`
	RelationDomain   string            `msgpack:"relation_domain,omitempty"`
	RelationLabel    string            `msgpack:"relation_label,omitempty"`
	RelationOnDelete OnDeleteBehaviour `msgpack:"relation_on_delete,omitempty"`

	// Sequence (KindSequence only)
	SequenceFormat string `msgpack:"sequence_format,omitempty"`
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

// Many2One declares a foreign-key relation field. relatedModel names the
// target model the same module-qualified way other cross-model
// references in this codebase do (e.g. "contacts.contact"). The
// declaring field's own name must end in "_id" — see go-sdk-reference.md
// §22 "Many2One".
func Many2One(relatedModel string) FieldDef {
	return FieldDef{Kind: KindMany2One, RelatedModel: relatedModel, RelationOnDelete: Restrict}
}

// Sequence declares a gapless, per-tenant counter field. format supports
// period tokens (e.g. "{year}") resolved against the acquisition time —
// see internal/engine/orm.ResolvePeriodKey.
func Sequence(format string) FieldDef {
	return FieldDef{Kind: KindSequence, SequenceFormat: format}
}

func (f FieldDef) Required() FieldDef           { f.IsRequired = true; return f }
func (f FieldDef) PrimaryKey() FieldDef         { f.IsPrimaryKey = true; return f }
func (f FieldDef) Default(expr string) FieldDef { f.DefaultExpr = &expr; return f }

// Primary marks this field as the model's display/label field (like
// Odoo's _rec_name or Frappe's title_field) — at most one field per model
// may declare it.
func (f FieldDef) Primary() FieldDef { f.IsPrimary = true; return f }

// Domain sets a SQL filter expression consumed by UI pickers and
// host.orm's relation-expansion query (Many2One fields only).
func (f FieldDef) Domain(expr string) FieldDef { f.RelationDomain = expr; return f }

// Label sets a Many2One field's display label metadata.
func (f FieldDef) Label(s string) FieldDef { f.RelationLabel = s; return f }

// OnDelete sets a Many2One field's FOREIGN KEY ON DELETE action.
func (f FieldDef) OnDelete(d OnDeleteBehaviour) FieldDef { f.RelationOnDelete = d; return f }
