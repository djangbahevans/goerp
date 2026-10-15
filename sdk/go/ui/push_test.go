package ui

import (
	"encoding/json/v2"
	"reflect"
	"testing"

	"github.com/vmihailenco/msgpack/v5"
)

func TestPushEncodingMatchesJSON(t *testing.T) {
	instructions := []Instruction{
		Toast("Done", Success),
		Toast("Persistent", Info, WithDuration(0)),
		Reload(),
		Reload(WithTarget("sales.invoice", "inv1")),
		Reload(WithListTarget("sales.invoice")),
		SetBadge("/sales/count", 0),
		SetBadge("/sales/count", 9007199254740993),
		Navigate("/sales"),
		OpenForm("sales.invoice", WithRecordID("inv1")),
		OpenDialog(Dialog{Title: "Confirm", Fields: []Field{{Name: "note", Type: FieldText}}, Values: map[string]any{"note": "hello"}}),
		CloseModal(),
		OpenURL("https://example.test"),
	}
	raw, err := encodePush(instructions)
	if err != nil {
		t.Fatal(err)
	}
	var decoded []map[string]any
	if err := msgpack.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	for index, instruction := range instructions {
		wantJSON, err := json.Marshal(instruction)
		if err != nil {
			t.Fatal(err)
		}
		gotJSON, err := json.Marshal(decoded[index])
		if err != nil {
			t.Fatal(err)
		}
		var got, want map[string]any
		if err := json.Unmarshal(gotJSON, &got); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(wantJSON, &want); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("instruction %d = %s, want %s", index, gotJSON, wantJSON)
		}
	}
	if got := decoded[6]["count"]; got != int64(9007199254740993) {
		t.Errorf("large count = %v (%T)", got, got)
	}
}

func TestPushEmptyArrayAndChannels(t *testing.T) {
	raw, err := encodePush(nil)
	if err != nil {
		t.Fatal(err)
	}
	var decoded []any
	if err := msgpack.Unmarshal(raw, &decoded); err != nil || decoded == nil || len(decoded) != 0 {
		t.Fatalf("empty instructions = %#v, %v", decoded, err)
	}
	if UserChannel("u1") != "ui:user:u1" || RoleChannel("admin") != "ui:role:admin" || RecordChannel("sales.invoice", "inv1") != "ui:record:sales.invoice:inv1" {
		t.Fatal("unexpected channel format")
	}
}

func TestPushZeroInstructionRetainsHostValidation(t *testing.T) {
	raw, err := encodePush([]Instruction{Reload(), {}})
	if err != nil {
		t.Fatal(err)
	}
	var decoded []map[string]any
	if err := msgpack.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded) != 2 || decoded[0]["type"] != "reload" || decoded[1] != nil {
		t.Fatalf("zero instruction encoding = %+v", decoded)
	}
}
