package model

type Schema struct {
	Types  []TypeDeclaration   `msgpack:"types,omitempty"`
	Models []*ModelDeclaration `msgpack:"models"`
}
