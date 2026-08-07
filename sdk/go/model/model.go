package model

type ModelDeclaration struct {
	Name        string       `msgpack:"name"`
	Table       string       `msgpack:"table,omitempty"`
	Label       string       `msgpack:"label,omitempty"`
	LabelPlural string       `msgpack:"label_plural,omitempty"`
	Fields      []NamedField `msgpack:"fields"`
	Indexes     []NamedIndex `msgpack:"indexes,omitempty"`
}

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
