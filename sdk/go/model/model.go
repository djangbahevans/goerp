package model

import "time"

type ModelDeclaration struct {
	Name                string       `msgpack:"name"`
	Table               string       `msgpack:"table,omitempty"`
	Label               string       `msgpack:"label,omitempty"`
	LabelPlural         string       `msgpack:"label_plural,omitempty"`
	Fields              []NamedField `msgpack:"fields"`
	Indexes             []NamedIndex `msgpack:"indexes,omitempty"`
	EnabledOps          []Op         `msgpack:"enabled_ops,omitempty"`
	Backend             ModelBackend `msgpack:"backend,omitempty"`
	TransientTTLSeconds int          `msgpack:"transient_ttl_seconds,omitempty"`
}

// ModelBackend selects what storage backend a model is read/written
// through. The zero value is the default: a Postgres table (Table sets
// its name). A non-default backend has no table for schema sync to
// create — see ToAtlasSchema (internal/engine/schema).
type ModelBackend string

const (
	// BackendVirtual routes a model's host.orm calls to a
	// module-registered backend function instead of a Postgres table —
	// see Virtual.
	BackendVirtual ModelBackend = "virtual"

	// BackendTransient routes a model's host.orm calls to a Redis-backed
	// key instead of a Postgres table — see Transient.
	BackendTransient ModelBackend = "transient"
)

type NamedField struct {
	Name string   `msgpack:"name"`
	Def  FieldDef `msgpack:"def"`
}

type NamedIndex struct {
	Name string   `msgpack:"name"`
	Def  IndexDef `msgpack:"def"`
}

type ModelOption func(*ModelDeclaration)

func Define(name string, opts ...ModelOption) *ModelDeclaration {
	d := &ModelDeclaration{Name: name}
	for _, opt := range opts {
		opt(d)
	}
	return d
}

func Table(tableName string) ModelOption {
	return func(d *ModelDeclaration) { d.Table = tableName }
}

// Virtual declares a model with no Postgres table at all — its
// host.orm calls route to a module-registered backend function instead
// (sdk/go/orm.RegisterVirtualBackend). Permitted only in modules of
// type: connector; declared elsewhere it is a load-time error
// (internal/engine/loader).
func Virtual() ModelOption {
	return func(d *ModelDeclaration) { d.Backend = BackendVirtual }
}

// Transient declares a model backed by Redis instead of a Postgres
// table — ephemeral, multi-step-wizard-shaped state with a sliding TTL,
// refreshed on every write. ttl is rounded down to the nearest second;
// zero or negative disables expiry checks at the SDK level (the engine
// still requires a positive TTL — a load-time error, not enforced here).
func Transient(ttl time.Duration) ModelOption {
	return func(d *ModelDeclaration) {
		d.Backend = BackendTransient
		d.TransientTTLSeconds = int(ttl / time.Second)
	}
}

func Label(singular string) ModelOption {
	return func(d *ModelDeclaration) { d.Label = singular }
}

func LabelPlural(plural string) ModelOption {
	return func(d *ModelDeclaration) { d.LabelPlural = plural }
}

func (d *ModelDeclaration) Field(name string, def FieldDef) *ModelDeclaration {
	d.Fields = append(d.Fields, NamedField{Name: name, Def: def})
	return d
}

func (d *ModelDeclaration) Index(name string, def IndexDef) *ModelDeclaration {
	d.Indexes = append(d.Indexes, NamedIndex{Name: name, Def: def})
	return d
}

// EnableOps allowlists which of the six reserved CRUD/list operations
// this model exposes — an allowlist, not a default: a model with no
// EnableOps call has no operations enabled. Route derivation, response
// envelopes, and collision handling against a hand-registered
// engine.Action are dispatch-side behavior, not part of this
// declaration.
func (d *ModelDeclaration) EnableOps(ops ...Op) *ModelDeclaration {
	d.EnabledOps = append(d.EnabledOps, ops...)
	return d
}

func (d *ModelDeclaration) WithStandardFields() *ModelDeclaration {
	return d.
		Field("id", UUID().PrimaryKey().Default("uuidv7()")).
		Field("tenant_id", UUID().Required()).
		Field("created_at", TimestampTZ().Required().Default("NOW()")).
		Field("updated_at", TimestampTZ().Required().Default("NOW()")).
		Field("deleted_at", TimestampTZ()).
		Field("created_by", UUID()).
		Field("etag", Text().Required().Default("''"))
}
