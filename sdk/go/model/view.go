package model

// ViewType is one of EnableViews' two synthesizable kinds — a
// marker-constant type mirroring Op (ops.go), not a struct a module
// author populates or overrides.
type ViewType struct {
	Name string `msgpack:"name"`
}

var (
	ListView = ViewType{Name: "list"}
	FormView = ViewType{Name: "form"}
)

// NavDeclaration is a model's .Nav() call, captured for the engine-side
// synthesizer (internal/engine/route's view/nav synthesis) to merge into
// the module's navigation tree — go-sdk-reference.md §22 "Nav".
type NavDeclaration struct {
	Group string `msgpack:"group"`
	Label string `msgpack:"label"`
	Order int    `msgpack:"order"`
}
