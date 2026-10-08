package model

// TypeDeclaration is a Postgres type declared alongside a schema's models.
// Only enum types are supported.
type TypeDeclaration struct {
	Name   string   `msgpack:"name"`
	Values []string `msgpack:"values"`
}

func EnumType(name string, values ...string) TypeDeclaration {
	return TypeDeclaration{Name: name, Values: values}
}
