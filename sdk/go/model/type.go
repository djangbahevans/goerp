package model

// TypeDeclaration is a Postgres type declared alongside a schema's models —
// currently only enum types (go-sdk-reference.md §22 "Enum types").
type TypeDeclaration struct {
	Name   string   `msgpack:"name"`
	Values []string `msgpack:"values"`
}

func EnumType(name string, values ...string) TypeDeclaration {
	return TypeDeclaration{Name: name, Values: values}
}
