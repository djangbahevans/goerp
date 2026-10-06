package def

import (
	"encoding/json/v2"
	"reflect"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/sdk/go/declare"
)

type addressPayload struct {
	City string `msgpack:"city"`
}

type schemaPayload struct {
	ContactID string            `msgpack:"contact_id"`
	Email     *string           `msgpack:"email"`
	Nickname  string            `msgpack:"nickname,omitempty"`
	Age       int32             `msgpack:"age"`
	Balance   uint64            `msgpack:"balance"`
	Score     float64           `msgpack:"score"`
	Active    bool              `msgpack:"active"`
	At        time.Time         `msgpack:"at"`
	Avatar    []byte            `msgpack:"avatar"`
	Tags      []string          `msgpack:"tags"`
	Meta      map[string]string `msgpack:"meta"`
	Address   addressPayload    `msgpack:"address"`
	Home      *addressPayload   `msgpack:"home"`
	Extra     any               `msgpack:"extra"`
	Skipped   string            `msgpack:"-"`
	Several   []addressPayload  `msgpack:"several"`
	Blob      blob              `msgpack:"blob"`
	ID        textID            `msgpack:"id"`
}

type blob []byte

type textID struct{}

func (textID) MarshalText() ([]byte, error) { return nil, nil }

func TestPayloadSchema(t *testing.T) {
	got := payloadSchema(reflect.TypeFor[schemaPayload]())
	want := map[string]any{
		"contact_id": "string",
		"email":      "string?",
		"nickname":   "string?",
		"age":        "int",
		"balance":    "uint",
		"score":      "float",
		"active":     "bool",
		"at":         "timestamp",
		"avatar":     "bytes",
		"tags":       "[]string",
		"meta":       "map",
		"address":    "object",
		"home":       "object?",
		"extra":      "any",
		"several":    "[]object",
		"blob":       "bytes",
		"id":         "any",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("payloadSchema =\n%v\nwant\n%v", got, want)
	}
}

func TestPayloadSchema_NonStructPayloadHasNone(t *testing.T) {
	if got := payloadSchema(reflect.TypeFor[map[string]any]()); got != nil {
		t.Errorf("payloadSchema = %v, want nil", got)
	}
}

func TestDefine_RecordsTheEventDeclaration(t *testing.T) {
	Define[addressPayload]("declared.address.updated", Version(3), Description("An address changed"), IdempotencyKeyField("city"))

	data, err := declare.Export()
	if err != nil {
		t.Fatal(err)
	}
	var byKind map[string][]EventDeclaration
	if err := json.Unmarshal(data, &byKind); err != nil {
		t.Fatal(err)
	}

	var got *EventDeclaration
	for _, d := range byKind[KindEvent] {
		if d.Name == "declared.address.updated" {
			got = &d
		}
	}
	want := EventDeclaration{
		Name: "declared.address.updated", Version: 3, Description: "An address changed",
		PayloadSchema: map[string]any{"city": "string"}, IdempotencyKeyField: "city",
	}
	if got == nil || !reflect.DeepEqual(*got, want) {
		t.Errorf("declaration = %+v, want %+v", got, want)
	}
}
