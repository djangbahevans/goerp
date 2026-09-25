package engine

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"reflect"
	"slices"
	"testing"
	"time"

	abi "github.com/djangbahevans/goerp/contract/abi/v1"
)

type typeDescAddress struct {
	City string `json:"city"`
}

type typeDescAudit struct {
	CreatedBy string `json:"created_by"`
	Note      string `json:"note"`
}

type typeDescContact struct {
	typeDescAudit
	ID        string           `json:"id"`
	Name      string           `json:"name,omitempty"`
	Email     *string          `json:"email"`
	Tags      []string         `json:"tags"`
	Meta      map[string]int   `json:"meta"`
	ByID      map[int]string   `json:"by_id"`
	Address   *typeDescAddress `json:"address"`
	CreatedAt time.Time        `json:"created_at"`
	Secret    string           `json:"-"`
	Untagged  bool
	Note      string         `json:"note"`
	Count     int64          `json:"count,string"`
	Raw       jsontext.Value `json:"raw"`
	Avatar    []byte         `json:"avatar"`
	Extra     any            `json:"extra,omitzero"`
	internal  string
	Lookup    map[string]string `json:"lookup,omitempty"`
}

type typeDescNode struct {
	Value    int             `json:"value"`
	Children []*typeDescNode `json:"children"`
}

type typeDescList []typeDescList

type typeDescTree map[string]typeDescTree

type typeDescOptionalEmbed struct {
	*typeDescAddress
	ID string `json:"id"`
}

type typeDescConflictA struct {
	Code string
}

type typeDescConflictB struct {
	Code string
}

type typeDescConflict struct {
	typeDescConflictA
	typeDescConflictB
	ID string `json:"id"`
}

func TestDescribeType(t *testing.T) {
	str := abi.TypeDesc{Kind: abi.TypeKindString}
	num := abi.TypeDesc{Kind: abi.TypeKindNumber}

	tests := []struct {
		name string
		typ  reflect.Type
		want TypeDesc
	}{
		{"string", reflect.TypeFor[string](), str},
		{"bool", reflect.TypeFor[bool](), TypeDesc{Kind: abi.TypeKindBoolean}},
		{"float", reflect.TypeFor[float64](), num},
		{"pointer is nullable", reflect.TypeFor[*int](), TypeDesc{Kind: abi.TypeKindNumber, Nullable: true}},
		{"slice is array", reflect.TypeFor[[]int](), TypeDesc{Kind: abi.TypeKindArray, Elem: &num}},
		{"string-keyed map is record", reflect.TypeFor[map[string]string](), TypeDesc{Kind: abi.TypeKindRecord, Elem: &str}},
		{"time.Time is string", reflect.TypeFor[time.Time](), str},
		{"interface is unknown", reflect.TypeFor[any](), TypeDesc{Kind: abi.TypeKindUnknown}},
		{"struct", reflect.TypeFor[typeDescContact](), TypeDesc{
			Kind: abi.TypeKindObject,
			Name: "typeDescContact",
			Fields: []FieldDesc{
				// Promoted from the embedded typeDescAudit; its "note" loses to
				// the shallower Note field declared on typeDescContact.
				{Name: "created_by", Type: str},
				{Name: "id", Type: str},
				{Name: "name", Type: str, Optional: true},
				{Name: "email", Type: TypeDesc{Kind: abi.TypeKindString, Nullable: true}},
				{Name: "tags", Type: TypeDesc{Kind: abi.TypeKindArray, Elem: &str}},
				{Name: "meta", Type: TypeDesc{Kind: abi.TypeKindRecord, Elem: &num}},
				{Name: "by_id", Type: TypeDesc{Kind: abi.TypeKindRecord, Elem: &str}},
				{Name: "address", Type: TypeDesc{
					Kind:     abi.TypeKindObject,
					Name:     "typeDescAddress",
					Fields:   []FieldDesc{{Name: "city", Type: str}},
					Nullable: true,
				}},
				{Name: "created_at", Type: str},
				{Name: "Untagged", Type: TypeDesc{Kind: abi.TypeKindBoolean}},
				{Name: "note", Type: str},
				{Name: "count", Type: str},
				{Name: "raw", Type: TypeDesc{Kind: abi.TypeKindUnknown}},
				{Name: "avatar", Type: str},
				{Name: "extra", Type: TypeDesc{Kind: abi.TypeKindUnknown}, Optional: true},
				{Name: "lookup", Type: TypeDesc{Kind: abi.TypeKindRecord, Elem: &str}, Optional: true},
			},
		}},
		{"recursive type is unknown where it recurs", reflect.TypeFor[typeDescNode](), TypeDesc{
			Kind: abi.TypeKindObject,
			Name: "typeDescNode",
			Fields: []FieldDesc{
				{Name: "value", Type: num},
				{Name: "children", Type: TypeDesc{
					Kind: abi.TypeKindArray,
					Elem: &TypeDesc{Kind: abi.TypeKindUnknown, Nullable: true},
				}},
			},
		}},
		{"recursive slice type is unknown where it recurs", reflect.TypeFor[typeDescList](), TypeDesc{
			Kind: abi.TypeKindArray,
			Elem: &TypeDesc{Kind: abi.TypeKindUnknown},
		}},
		{"recursive map type is unknown where it recurs", reflect.TypeFor[typeDescTree](), TypeDesc{
			Kind: abi.TypeKindRecord,
			Elem: &TypeDesc{Kind: abi.TypeKindUnknown},
		}},
		{"non-object map key is unknown", reflect.TypeFor[map[bool]string](), TypeDesc{Kind: abi.TypeKindUnknown}},
		{"embedded pointer's promoted fields are optional", reflect.TypeFor[typeDescOptionalEmbed](), TypeDesc{
			Kind: abi.TypeKindObject,
			Name: "typeDescOptionalEmbed",
			Fields: []FieldDesc{
				{Name: "city", Type: str, Optional: true},
				{Name: "id", Type: str},
			},
		}},
		{"conflicting promoted fields at one depth are dropped", reflect.TypeFor[typeDescConflict](), TypeDesc{
			Kind:   abi.TypeKindObject,
			Name:   "typeDescConflict",
			Fields: []FieldDesc{{Name: "id", Type: str}},
		}},
		{"anonymous struct has no name", reflect.TypeFor[struct {
			OK bool `json:"ok"`
		}](), TypeDesc{
			Kind:   abi.TypeKindObject,
			Fields: []FieldDesc{{Name: "ok", Type: TypeDesc{Kind: abi.TypeKindBoolean}}},
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := describeType(tt.typ); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("describeType(%v)\n got %+v\nwant %+v", tt.typ, got, tt.want)
			}
		})
	}
}

// The field list must match what encoding/json/v2 actually sends for the
// type, in the same order.
func TestDescribeType_FieldsMatchEncodingJSON(t *testing.T) {
	encoded, err := json.Marshal(typeDescContact{internal: "x", Name: "n", Lookup: map[string]string{"k": "v"}, Extra: 1, Raw: jsontext.Value(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	var sent []string
	dec := jsontext.NewDecoder(bytes.NewReader(encoded))
	if _, err := dec.ReadToken(); err != nil {
		t.Fatal(err)
	}
	for dec.PeekKind() != '}' {
		key, err := dec.ReadToken()
		if err != nil {
			t.Fatal(err)
		}
		sent = append(sent, key.String())
		if err := dec.SkipValue(); err != nil {
			t.Fatal(err)
		}
	}

	var described []string
	for _, f := range describeType(reflect.TypeFor[typeDescContact]()).Fields {
		described = append(described, f.Name)
	}
	if !slices.Equal(described, sent) {
		t.Errorf("described fields %v, encoding/json/v2 sent %v", described, sent)
	}
}

func TestBodyAndReturns_RecordTypesOnRouteAndAction(t *testing.T) {
	r := withRouter(t)

	POST("/contacts/merge", func(*Request) *Response { return nil },
		Body[typeDescAddress](), Returns[typeDescContact]())
	Action("contacts.contact", "archive", func(*Request) *Response { return nil },
		Body[typeDescAddress](), Returns[typeDescNode]())

	decls := routeDeclarations(r.routes)
	for i, want := range []struct{ req, resp reflect.Type }{
		{reflect.TypeFor[typeDescAddress](), reflect.TypeFor[typeDescContact]()},
		{reflect.TypeFor[typeDescAddress](), reflect.TypeFor[typeDescNode]()},
	} {
		d := decls[i]
		if d.RequestType == nil || !reflect.DeepEqual(*d.RequestType, describeType(want.req)) {
			t.Errorf("decls[%d].RequestType = %+v, want %v described", i, d.RequestType, want.req)
		}
		if d.ResponseType == nil || !reflect.DeepEqual(*d.ResponseType, describeType(want.resp)) {
			t.Errorf("decls[%d].ResponseType = %+v, want %v described", i, d.ResponseType, want.resp)
		}
	}
}

func TestRouteDeclaration_WithoutBodyOrReturnsIsUnchangedOnTheWire(t *testing.T) {
	r := withRouter(t)
	GET("/contacts", func(*Request) *Response { return nil }, Requires("contacts:contact:read"))

	got, err := marshal(routeDeclarations(r.routes)[0])
	if err != nil {
		t.Fatal(err)
	}
	want, err := marshal(RouteDeclaration{
		Method:       "GET",
		Path:         "/contacts",
		Auth:         string(AuthRequired),
		Permissions:  []string{"contacts:contact:read"},
		MaxBodyBytes: defaultMaxBodyBytes,
		TimeoutMs:    int(defaultTimeout / time.Millisecond),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("wire bytes differ from a declaration with no body/response types")
	}

	var asMap map[string]any
	if err := unmarshal(got, &asMap); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"request_type", "response_type"} {
		if _, present := asMap[key]; present {
			t.Errorf("key %q present without engine.Body/engine.Returns", key)
		}
	}
}
