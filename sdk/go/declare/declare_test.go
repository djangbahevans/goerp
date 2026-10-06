package declare

import (
	"bytes"
	"testing"
)

func withEmptyRegistry(t *testing.T) {
	t.Helper()
	orig := entries
	entries = nil
	t.Cleanup(func() { entries = orig })
}

type widget struct {
	Name string `json:"name"`
	Rank int    `json:"rank,omitzero"`
}

func TestExport_GroupsDeclarationsByKindInRegistrationOrder(t *testing.T) {
	withEmptyRegistry(t)
	Add("widget", widget{Name: "b", Rank: 2})
	Add("gadget", map[string]any{"id": 1})
	Add("widget", widget{Name: "a"})

	got, err := Export()
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	want := `{"gadget":[{"id":1}],"widget":[{"name":"b","rank":2},{"name":"a"}]}`
	if string(got) != want {
		t.Errorf("Export = %s, want %s", got, want)
	}
}

func TestExport_EmptyRegistryIsAnEmptyObject(t *testing.T) {
	withEmptyRegistry(t)

	got, err := Export()
	if err != nil || string(got) != "{}" {
		t.Fatalf("Export = %s, %v; want {}", got, err)
	}
}

func TestExport_UnencodableDeclarationErrors(t *testing.T) {
	withEmptyRegistry(t)
	Add("bad", func() {})

	if _, err := Export(); err == nil {
		t.Fatal("Export succeeded for a declaration that cannot be encoded")
	}
}

func TestWriteTo_WritesTheExport(t *testing.T) {
	withEmptyRegistry(t)
	Add("widget", widget{Name: "a"})

	var buf bytes.Buffer
	if err := WriteTo(&buf); err != nil {
		t.Fatalf("WriteTo: %v", err)
	}
	if got, want := buf.String(), `{"widget":[{"name":"a"}]}`; got != want {
		t.Errorf("WriteTo = %s, want %s", got, want)
	}
}
