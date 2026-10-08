package model

// ViewType is a view kind EnableViews can generate.
type ViewType struct {
	Name string `msgpack:"name"`
}

var (
	ListView = ViewType{Name: "list"}
	FormView = ViewType{Name: "form"}
)

// NavDeclaration is a model's .Nav() entry, which the engine merges into
// the module's navigation tree.
type NavDeclaration struct {
	Group string `msgpack:"group"`
	Label string `msgpack:"label"`
	Order int    `msgpack:"order"`
}
