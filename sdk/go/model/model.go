package model

import (
	"strings"
	"time"
)

// Named is implemented by every generated model struct. It lives here, below
// both engine and orm, so each can constrain a type parameter to a model
// and derive its resource name from the type instead of a string.
type Named interface {
	ResourceName() string
}

type ModelDeclaration struct {
	Name                string            `msgpack:"name"`
	Table               string            `msgpack:"table,omitempty"`
	Label               string            `msgpack:"label,omitempty"`
	LabelPlural         string            `msgpack:"label_plural,omitempty"`
	Fields              []NamedField      `msgpack:"fields"`
	Indexes             []NamedIndex      `msgpack:"indexes,omitempty"`
	EnabledOps          []Op              `msgpack:"enabled_ops,omitempty"`
	EnabledViews        []ViewType        `msgpack:"enabled_views,omitempty"`
	NavDecl             *NavDeclaration   `msgpack:"nav,omitempty"`
	Backend             ModelBackend      `msgpack:"backend,omitempty"`
	TransientTTLSeconds int               `msgpack:"transient_ttl_seconds,omitempty"`
	RoutePrefixOverride string            `msgpack:"route_prefix,omitempty"`
	Shareable           bool              `msgpack:"shareable,omitempty"`
	SharePerms          []SharePermission `msgpack:"share_perms,omitempty"`
	OnCreateEvent       *LifecycleEvent   `msgpack:"on_create,omitempty"`
	OnUpdateEvent       *LifecycleEvent   `msgpack:"on_update,omitempty"`
	OnDeleteEvent       *LifecycleEvent   `msgpack:"on_delete,omitempty"`
}

// ModelBackend selects what storage backend a model is read/written
// through. The zero value is the default: a Postgres table (Table sets
// its name). A non-default backend has no table.
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

// QualifiedName returns the declaration's module-qualified name,
// "<module>.<resource>", whether Name is spelled bare or already carries
// the module prefix.
func (d ModelDeclaration) QualifiedName(module string) string {
	if strings.HasPrefix(d.Name, module+".") {
		return d.Name
	}
	return module + "." + d.Name
}

// ResourceName returns the declaration's bare resource name, the last
// dotted segment of Name.
func (d ModelDeclaration) ResourceName() string {
	_, name, ok := strings.CutLast(d.Name, ".")
	if !ok {
		return d.Name
	}
	return name
}

func Table(tableName string) ModelOption {
	return func(d *ModelDeclaration) { d.Table = tableName }
}

// Virtual declares a model with no Postgres table: its ORM calls route to
// backend functions registered with orm.RegisterVirtualBackend. Only
// connector modules may declare one; elsewhere the module fails to load.
func Virtual() ModelOption {
	return func(d *ModelDeclaration) { d.Backend = BackendVirtual }
}

// Transient declares a model backed by Redis instead of a Postgres
// table — ephemeral, multi-step-wizard-shaped state with a sliding TTL,
// refreshed on every write. ttl is rounded down to the nearest second;
// the engine rejects a zero or negative ttl at module load.
func Transient(ttl time.Duration) ModelOption {
	return func(d *ModelDeclaration) {
		d.Backend = BackendTransient
		d.TransientTTLSeconds = int(ttl / time.Second)
	}
}

// SharePermission is the access level an ad hoc per-record grant confers.
// ReadShare grants read access only; WriteShare grants read and write.
type SharePermission string

const (
	ReadShare  SharePermission = "read"
	WriteShare SharePermission = "write"
)

// Shareable opts a model into ad hoc per-record grants managed through the
// built-in /_meta/shares endpoint, independent of roles and ABAC rules.
// perms lists the grant levels the endpoint accepts for this model.
func Shareable(perms ...SharePermission) ModelOption {
	return func(d *ModelDeclaration) {
		d.Shareable = true
		d.SharePerms = perms
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

// EnableOps allowlists which of the seven reserved CRUD/list operations
// this model exposes. A model with no EnableOps call exposes none.
func (d *ModelDeclaration) EnableOps(ops ...Op) *ModelDeclaration {
	d.EnabledOps = append(d.EnabledOps, ops...)
	return d
}

// EnableViews allowlists which generated views this model gets. Each view
// requires the matching operations in EnableOps; the module fails to load
// otherwise.
func (d *ModelDeclaration) EnableViews(views ...ViewType) *ModelDeclaration {
	d.EnabledViews = append(d.EnabledViews, views...)
	return d
}

// Nav registers a navigation entry for this model's generated list view.
// Requires EnableViews(ListView); the module fails to load otherwise.
func (d *ModelDeclaration) Nav(group, label string, order int) *ModelDeclaration {
	d.NavDecl = &NavDeclaration{Group: group, Label: label, Order: order}
	return d
}

// RoutePrefix overrides the derived plural-segment path for this model's
// EnableOps-registered routes — the automatic derivation pluralizes
// LabelPlural (or the model name if LabelPlural isn't set); use this
// when that derivation is wrong (an irregular plural, or a path that
// collides with an existing hand-written prefix).
func (d *ModelDeclaration) RoutePrefix(path string) *ModelDeclaration {
	d.RoutePrefixOverride = path
	return d
}

// WithStandardFields adds the seven engine-managed columns, all Readonly:
// id, created_at, updated_at, deleted_at and etag are assigned by the
// database, and tenant_id and created_by by the engine from the request.
func (d *ModelDeclaration) WithStandardFields() *ModelDeclaration {
	return d.
		Field("id", UUID().PrimaryKey().Default("uuidv7()").Readonly()).
		Field("tenant_id", UUID().Required().Readonly()).
		Field("created_at", TimestampTZ().Required().Default("NOW()").Readonly()).
		Field("updated_at", TimestampTZ().Required().Default("NOW()").Readonly()).
		Field("deleted_at", TimestampTZ().Readonly()).
		Field("created_by", UUID().Readonly()).
		Field("etag", Text().Required().Default("''").Readonly())
}
