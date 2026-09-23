package orm

import (
	"reflect"
	"testing"
)

// valuesTestModel is a hand-written stand-in for what goerp module
// generate (issue #977) emits — Model + scanner, plus a couple of Field
// descriptors — enough to exercise NewValues/Set/SetBytes and Create/
// Write/FirstOrCreate/Unlink's own type parameters without a real
// generated struct.
type valuesTestModel struct {
	ID   string
	Name string
	Note []byte
}

func (valuesTestModel) ResourceName() string { return "test.valuesmodel" }

func (m *valuesTestModel) Scan(row map[string]any) error {
	if v, ok := row["id"]; ok {
		s, ok := v.(string)
		if !ok {
			return NewDecodeError("valuesTestModel", "ID", "string", v)
		}
		m.ID = s
	}
	if v, ok := row["name"]; ok {
		s, ok := v.(string)
		if !ok {
			return NewDecodeError("valuesTestModel", "Name", "string", v)
		}
		m.Name = s
	}
	return nil
}

var (
	valuesTestModelName = NewField[valuesTestModel, string]("name")
	valuesTestModelNote = NewBytesField[valuesTestModel]("note")
)

func TestNewValues_StartsEmpty(t *testing.T) {
	v := NewValues[valuesTestModel]()
	if len(v.raw()) != 0 {
		t.Errorf("raw() = %v, want empty", v.raw())
	}
}

func TestSet_AddsFieldAndReturnsSameValues(t *testing.T) {
	v := NewValues[valuesTestModel]()
	got := Set(v, valuesTestModelName, "Acme")

	if got != v {
		t.Error("Set did not return the same *Values it was given")
	}
	if v.raw()["name"] != "Acme" {
		t.Errorf(`raw()["name"] = %v, want "Acme"`, v.raw()["name"])
	}
}

func TestSet_Chains(t *testing.T) {
	v := Set(NewValues[valuesTestModel](), valuesTestModelName, "Acme")
	want := map[string]any{"name": "Acme"}
	if !reflect.DeepEqual(v.raw(), want) {
		t.Errorf("raw() = %v, want %v", v.raw(), want)
	}
}

func TestSetBytes_AddsField(t *testing.T) {
	v := SetBytes(NewValues[valuesTestModel](), valuesTestModelNote, []byte("hi"))
	got, ok := v.raw()["note"].([]byte)
	if !ok || string(got) != "hi" {
		t.Errorf(`raw()["note"] = %v, want []byte("hi")`, v.raw()["note"])
	}
}

func TestResourceName_DerivesFromZeroValue(t *testing.T) {
	if got := resourceName[valuesTestModel](); got != "test.valuesmodel" {
		t.Errorf("resourceName = %q, want %q", got, "test.valuesmodel")
	}
}

// TestValues_NilRawIsSafe pins raw()'s nil-receiver handling — Unlink
// takes no Values at all, but Create/Write/etc.'s own zero-value
// *Values[T] (never constructed via NewValues) still shouldn't panic
// building the hostcall input.
func TestValues_NilRawIsSafe(t *testing.T) {
	var v *Values[valuesTestModel]
	if got := v.raw(); got != nil {
		t.Errorf("raw() on a nil *Values = %v, want nil", got)
	}
}

// TestSet_OnZeroValueValuesDoesNotPanic pins Set/SetBytes against a
// &Values[T]{} built without NewValues — m is unexported but the struct
// itself isn't, so this is a legal zero-value composite literal from
// outside the package, and Set assigning into a nil map would otherwise
// panic instead of just working.
func TestSet_OnZeroValueValuesDoesNotPanic(t *testing.T) {
	v := &Values[valuesTestModel]{}
	Set(v, valuesTestModelName, "Acme")
	if v.raw()["name"] != "Acme" {
		t.Errorf(`raw()["name"] = %v, want "Acme"`, v.raw()["name"])
	}
}
