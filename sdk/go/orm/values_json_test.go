package orm

import (
	"encoding/json/v2"
	"errors"
	"reflect"
	"testing"
	"time"
)

type valuesJSONModel struct{}

func (valuesJSONModel) ResourceName() string { return "test.valuesjson" }

func (valuesJSONModel) ValueFields() map[string]ValueDecoder {
	return map[string]ValueDecoder{
		"id":       nil,
		"name":     DecodeValue[string],
		"quantity": DecodeValue[int32],
		"active":   DecodeValue[bool],
		"due":      DecodeDate,
		"meta":     DecodeJSONB,
		"blob":     DecodeValue[[]byte],
	}
}

func decodeValues(t *testing.T, body string) (*Values[valuesJSONModel], error) {
	t.Helper()
	var v *Values[valuesJSONModel]
	err := json.Unmarshal([]byte(body), &v)
	return v, err
}

func TestValuesUnmarshal_KeepsOnlyMembersSent(t *testing.T) {
	v, err := decodeValues(t, `{"name":"Acme","quantity":3,"due":"2026-10-08","meta":{"a": [1]},"blob":"aGk="}`)
	if err != nil {
		t.Fatalf("Unmarshal error = %v", err)
	}
	want := map[string]any{
		"name":     "Acme",
		"quantity": int32(3),
		"due":      time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC),
		"meta":     []byte(`{"a":[1]}`),
		"blob":     []byte("hi"),
	}
	if !reflect.DeepEqual(v.raw(), want) {
		t.Errorf("raw() = %#v, want %#v", v.raw(), want)
	}
}

func TestValuesUnmarshal_EmptyObjectIsEmptyValues(t *testing.T) {
	v, err := decodeValues(t, `{}`)
	if err != nil || v == nil || len(v.raw()) != 0 {
		t.Fatalf("Unmarshal = %v, %v, want an empty Values", v, err)
	}
}

func TestValuesUnmarshal_NullStoresNil(t *testing.T) {
	v, err := decodeValues(t, `{"name":null,"due":null,"meta":null}`)
	if err != nil {
		t.Fatalf("Unmarshal error = %v", err)
	}
	for _, name := range []string{"name", "due", "meta"} {
		if got, ok := v.raw()[name]; !ok || got != nil {
			t.Errorf("raw()[%q] = %v, %v, want a present nil", name, got, ok)
		}
	}
}

func TestValuesUnmarshal_RejectsMembers(t *testing.T) {
	tests := []struct {
		name, body, field, message string
	}{
		{"unknown field", `{"name":"x","nope":1}`, "nope", "unknown field"},
		{"read-only field", `{"id":"1"}`, "id", "read-only field"},
		{"string for int", `{"quantity":"3"}`, "quantity", "cannot use a JSON string as int32"},
		{"fraction for int", `{"quantity":3.5}`, "quantity", ""},
		{"int32 overflow", `{"quantity":3000000000}`, "quantity", ""},
		{"number for string", `{"name":4}`, "name", "cannot use a JSON number as string"},
		{"object for bool", `{"active":{}}`, "active", "cannot use a JSON object as bool"},
		{"bad date", `{"due":"yesterday"}`, "due", "must be a YYYY-MM-DD date or an RFC 3339 timestamp"},
		{"number for date", `{"due":5}`, "due", "cannot use a JSON number as string"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := decodeValues(t, tt.body)
			ve, ok := errors.AsType[*ValueError](err)
			if !ok {
				t.Fatalf("error = %v, want a *ValueError", err)
			}
			if ve.Field != tt.field || (tt.message != "" && ve.Message != tt.message) {
				t.Errorf("ValueError = %+v, want field %q message %q", ve, tt.field, tt.message)
			}
		})
	}
}

func TestValuesUnmarshal_MalformedBodyIsNotAValueError(t *testing.T) {
	for _, body := range []string{`[1]`, `"x"`, `{"name":`, `{"name":"a","name":"b"}`} {
		_, err := decodeValues(t, body)
		if err == nil {
			t.Errorf("Unmarshal(%s) error = nil, want an error", body)
			continue
		}
		if _, ok := errors.AsType[*ValueError](err); ok {
			t.Errorf("Unmarshal(%s) error = %v, want a non-field error", body, err)
		}
	}
}

func TestValuesUnmarshal_ModelWithoutValueFieldsRejectsEveryMember(t *testing.T) {
	var v *Values[valuesTestModel]
	err := json.Unmarshal([]byte(`{"name":"x"}`), &v)
	if ve, ok := errors.AsType[*ValueError](err); !ok || ve.Field != "name" {
		t.Fatalf("error = %v, want a *ValueError for name", err)
	}
}

func TestDecodeDate_AcceptsTimestamp(t *testing.T) {
	v, err := decodeValues(t, `{"due":"2026-10-08T10:30:00Z"}`)
	if err != nil {
		t.Fatalf("Unmarshal error = %v", err)
	}
	want := time.Date(2026, 10, 8, 10, 30, 0, 0, time.UTC)
	if got, ok := v.raw()["due"].(time.Time); !ok || !got.Equal(want) {
		t.Errorf(`raw()["due"] = %v, want %v`, v.raw()["due"], want)
	}
}
