package model

type Schema struct {
	Models []*ModelDeclaration `msgpack:"models"`
}
