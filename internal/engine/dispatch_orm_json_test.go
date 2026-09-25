package engine

import (
	"encoding/json/jsontext"
	"reflect"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/sdk/go/model"
)

func ormJSONTestDecl() model.ModelDeclaration {
	return *model.Define("testmodule.thing").
		Field("doc", model.JSONB()).
		Field("blob", model.Bytea()).
		Field("day", model.Date()).
		Field("at", model.TimestampTZ()).
		Field("name", model.Text())
}

func TestORMRecordToJSON(t *testing.T) {
	at := time.Date(2024, 3, 15, 10, 30, 0, 0, time.UTC)
	westOfUTC := time.FixedZone("GMT-4", -4*60*60)
	got := ormRecordToJSON(ormJSONTestDecl(), map[string]any{
		"doc":  []byte(`{"k": 1}`),
		"blob": []byte("hi"),
		"day":  time.Date(2024, 3, 15, 0, 0, 0, 0, time.UTC),
		"at":   at.In(westOfUTC),
		"name": "n",
	})
	want := map[string]any{
		"doc":  jsontext.Value(`{"k": 1}`),
		"blob": []byte("hi"),
		"day":  "2024-03-15",
		"at":   at,
		"name": "n",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ormRecordToJSON() = %#v, want %#v", got, want)
	}
	if ormRecordToJSON(ormJSONTestDecl(), nil) != nil {
		t.Error("ormRecordToJSON(nil) != nil")
	}
}

func TestORMRecordFromJSON(t *testing.T) {
	got, err := ormRecordFromJSON(ormJSONTestDecl(), map[string]any{
		"doc":  "just a string",
		"blob": "aGk=",
		"day":  "2024-03-15",
		"name": "n",
	})
	if err != nil {
		t.Fatalf("ormRecordFromJSON() error = %v", err)
	}
	want := map[string]any{"doc": []byte(`"just a string"`), "blob": []byte("hi"), "day": "2024-03-15", "name": "n"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ormRecordFromJSON() = %#v, want %#v", got, want)
	}

	nulls, err := ormRecordFromJSON(ormJSONTestDecl(), map[string]any{"doc": nil, "blob": nil})
	if err != nil || nulls["doc"] != nil || nulls["blob"] != nil {
		t.Errorf("ormRecordFromJSON(nulls) = %#v, %v, want nulls kept", nulls, err)
	}

	for _, blob := range []any{"not base64!", float64(1)} {
		if _, err := ormRecordFromJSON(ormJSONTestDecl(), map[string]any{"blob": blob}); err == nil {
			t.Errorf("ormRecordFromJSON(blob=%#v) error = nil, want an error naming blob", blob)
		}
	}
}
