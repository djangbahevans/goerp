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
	KindDynamicLink
	KindOne2Many
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

// ReadBehaviourKind identifies which DeniedReadBehaviour a field declares —
// see model.Omit, model.Nullify, model.Mask.
type ReadBehaviourKind int

const (
	ReadKindOmit ReadBehaviourKind = iota
	ReadKindNullify
	ReadKindMask
)

// DeniedReadBehaviour controls what a caller who lacks a field's read
// permission sees in the response (go-sdk-reference.md §22 "Access
// control", auth-internals.md §12 "Read behaviours"). Constructed via
// model.Omit, model.Nullify, or model.Mask(pattern) — never directly.
type DeniedReadBehaviour struct {
	Kind    ReadBehaviourKind `msgpack:"kind"`
	Pattern string            `msgpack:"pattern,omitempty"` // Mask only: {last4}, {first2}, {length}
}

var (
	// Omit removes the field from the response entirely — the client
	// doesn't know the field exists.
	Omit = DeniedReadBehaviour{Kind: ReadKindOmit}

	// Nullify keeps the field present in the response with a null value.
	Nullify = DeniedReadBehaviour{Kind: ReadKindNullify}
)

// Mask keeps the field present with its value partially replaced per
// pattern's substitutions ({last4}, {first2}, {length}), e.g.
// "****{last4}".
func Mask(pattern string) DeniedReadBehaviour {
	return DeniedReadBehaviour{Kind: ReadKindMask, Pattern: pattern}
}

// DeniedWriteBehaviour controls what happens when a create/write request
// includes a field the caller lacks write permission for
// (go-sdk-reference.md §22 "Access control", auth-internals.md §12
// "Write behaviours").
type DeniedWriteBehaviour int

const (
	// Reject fails the entire request with 403 field_write_denied,
	// naming the specific field.
	Reject DeniedWriteBehaviour = iota

	// Ignore silently strips the field from the request before the
	// module handler sees it.
	Ignore
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

	// Tree (Many2One only — go-sdk-reference.md §22 "Tree"). Requires
	// RelatedModel to be the declaring model's own qualified name;
	// enforced at schema-sync time (the builder doesn't know its own
	// model's name yet), not here.
	IsTree bool `msgpack:"is_tree,omitempty"`

	// Sequence (KindSequence only)
	SequenceFormat string `msgpack:"sequence_format,omitempty"`

	// DynamicLink (KindDynamicLink only) — go-sdk-reference.md §22
	// "DynamicLink". ReferenceTypeField names the sibling Selection field
	// whose value picks this record's target model per row.
	ReferenceTypeField string `msgpack:"reference_type_field,omitempty"`

	// One2Many (KindOne2Many only) — go-sdk-reference.md §22 "One2Many".
	// RelatedModel (shared with Many2One above) names the child model;
	// InverseField names the Many2One field on that child model that
	// points back at the declaring model. No backing column: the data
	// lives entirely on the child's own Many2One column.
	InverseField string `msgpack:"inverse_field,omitempty"`

	// Computed field recomputation (go-sdk-reference.md §22 "Computed
	// field recomputation")
	IsComputed bool     `msgpack:"is_computed,omitempty"`
	ComputeFn  string   `msgpack:"compute_fn,omitempty"` // WASM export name the engine dispatches to
	IsStored   bool     `msgpack:"is_stored,omitempty"`  // Store(true): persisted, recomputed on write. Store(false): computed fresh on read
	DependsOn  []string `msgpack:"depends_on,omitempty"` // field names, or "relField.remoteField" through a Many2One

	// Field-level access control (go-sdk-reference.md §22 "Access
	// control", manifest-spec.md §8a, auth-internals.md §12). A field
	// with no ReadPermission has no read restriction — that's the
	// absence of a rule, not an explicit allow. DeniedRead/DeniedWrite
	// are pointers so "not declared" is distinguishable from an explicit
	// zero-value behaviour (Omit / Reject).
	ReadPermission  string                `msgpack:"read_permission,omitempty"`
	WritePermission string                `msgpack:"write_permission,omitempty"`
	DeniedRead      *DeniedReadBehaviour  `msgpack:"denied_read,omitempty"`
	DeniedWrite     *DeniedWriteBehaviour `msgpack:"denied_write,omitempty"`
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

// DynamicLink declares a polymorphic relation field — its target model
// varies per record, named by the sibling Selection field
// referenceTypeField (go-sdk-reference.md §22 "DynamicLink"). No FK: a
// Postgres FK can only reference one table, and the target table varies
// per row.
func DynamicLink(referenceTypeField string) FieldDef {
	return FieldDef{Kind: KindDynamicLink, ReferenceTypeField: referenceTypeField}
}

// One2Many declares the inverse side of a Many2One relation — a virtual
// field with no backing column. relatedModel names the child model
// (module-qualified); inverseField names the Many2One field on that
// child model that points back at this model — see
// go-sdk-reference.md §22 "One2Many".
func One2Many(relatedModel, inverseField string) FieldDef {
	return FieldDef{Kind: KindOne2Many, RelatedModel: relatedModel, InverseField: inverseField}
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

// Tree marks a self-referential Many2One field as a hierarchy field —
// go-sdk-reference.md §22 "Tree". The engine auto-declares a companion
// {field}_path ltree column and maintains it on create/reparent.
func (f FieldDef) Tree() FieldDef { f.IsTree = true; return f }

// Computed declares this field as engine-recomputed rather than
// caller-settable — fnName is the WASM export name (go-sdk-reference.md
// §22 "Computed field recomputation") the engine dispatches to. Pair with
// .Store() and .Depends() to complete the declaration.
func (f FieldDef) Computed(fnName string) FieldDef {
	f.IsComputed = true
	f.ComputeFn = fnName
	return f
}

// Store selects whether a Computed field persists its value (true,
// recomputed synchronously on any orm.Create/Write/WriteMany touching a
// .Depends() field) or is computed fresh on every orm.Read/search_read
// (false, never persisted).
func (f FieldDef) Store(stored bool) FieldDef { f.IsStored = stored; return f }

// Depends names the fields this Computed field's value depends on —
// either a bare field name on the same record, or "relField.remoteField"
// through either a Many2One field named relField+"_id" on the same model
// (remoteField names a field on the Many2One's target model) or a
// One2Many field named relField on the same model (remoteField names a
// field on the One2Many's child model).
func (f FieldDef) Depends(paths ...string) FieldDef { f.DependsOn = paths; return f }

// AccessOpt configures a field's Access() permission requirements —
// model.AccessRead(permission), model.AccessWrite(permission).
type AccessOpt func(*FieldDef)

// AccessRead requires permission to read this field's value.
func AccessRead(permission string) AccessOpt {
	return func(f *FieldDef) { f.ReadPermission = permission }
}

// AccessWrite requires permission to set this field's value via
// create/write.
func AccessWrite(permission string) AccessOpt {
	return func(f *FieldDef) { f.WritePermission = permission }
}

// Access declares field-level read/write permission requirements —
// go-sdk-reference.md §22 "Access control". Pair with OnDeniedRead/
// OnDeniedWrite to say what happens when the caller lacks the
// declared permission.
func (f FieldDef) Access(opts ...AccessOpt) FieldDef {
	for _, opt := range opts {
		opt(&f)
	}
	return f
}

// OnDeniedRead sets the behaviour applied when the caller lacks this
// field's read permission: model.Omit, model.Nullify, or
// model.Mask(pattern).
func (f FieldDef) OnDeniedRead(b DeniedReadBehaviour) FieldDef {
	f.DeniedRead = &b
	return f
}

// OnDeniedWrite sets the behaviour applied when a create/write request
// includes this field and the caller lacks its write permission:
// model.Reject or model.Ignore.
func (f FieldDef) OnDeniedWrite(b DeniedWriteBehaviour) FieldDef {
	f.DeniedWrite = &b
	return f
}
