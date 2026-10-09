package model

// ModelExtension adds fields and indexes to a qualified, table-backed model.
// Fields must be nullable or have a default; existing fields cannot be redefined.
type ModelExtension struct {
	Model   string       `msgpack:"model"`
	Fields  []NamedField `msgpack:"fields,omitempty"`
	Indexes []NamedIndex `msgpack:"indexes,omitempty"`
}

// Extend targets a model named "module.resource". The module manifest must
// declare that model in schema.extends_models and its owner in extends_module.
func Extend(qualifiedModel string) ModelExtension {
	return ModelExtension{Model: qualifiedModel}
}

// Field adds a new field with its storage and access rules.
func (e ModelExtension) Field(name string, def FieldDef) ModelExtension {
	e.Fields = append(e.Fields, NamedField{Name: name, Def: def})

	return e
}

// Index adds an index over fields of the target model, including added fields.
func (e ModelExtension) Index(name string, def IndexDef) ModelExtension {
	e.Indexes = append(e.Indexes, NamedIndex{Name: name, Def: def})

	return e
}
