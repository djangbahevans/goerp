package orm

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"time"
)

// ValueDecoder reads one JSON member value from dec as a field's declared
// Go type and returns it in the form orm.Set stores. A nil decoder in a
// model's ValueFields marks a read-only field.
type ValueDecoder func(dec *jsontext.Decoder) (any, error)

// valueFielder is implemented by goerp module generate's model structs:
// ValueFields maps every column name the struct holds to the decoder for
// its writable members.
type valueFielder interface {
	ValueFields() map[string]ValueDecoder
}

// ValueError reports a request-body member that Values[T] rejects: an
// unknown or read-only field, or a JSON value that does not fit the field's
// Go type.
type ValueError struct {
	Field   string
	Message string
}

func (e *ValueError) Error() string {
	return fmt.Sprintf("field %q: %s", e.Field, e.Message)
}

// UnmarshalJSONFrom decodes a JSON object into v, keeping only the members
// sent. An unknown or read-only field, or a value that does not fit the
// field's declared Go type, fails with a *ValueError; a JSON null stores a
// nil value.
func (v *Values[T]) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
	var zero T
	var fields map[string]ValueDecoder
	if vf, ok := any(zero).(valueFielder); ok {
		fields = vf.ValueFields()
	}

	if dec.PeekKind() != '{' {
		return errors.New("values body must be a JSON object")
	}
	if _, err := dec.ReadToken(); err != nil {
		return err
	}
	m := make(map[string]any)
	for dec.PeekKind() != '}' {
		tok, err := dec.ReadToken()
		if err != nil {
			return err
		}
		name := tok.String()
		decode, known := fields[name]
		switch {
		case !known:
			return &ValueError{Field: name, Message: "unknown field"}
		case decode == nil:
			return &ValueError{Field: name, Message: "read-only field"}
		}
		value, err := decode(dec)
		if err != nil {
			if _, syntactic := errors.AsType[*jsontext.SyntacticError](err); syntactic {
				return err
			}
			return &ValueError{Field: name, Message: valueMessage(err)}
		}
		m[name] = value
	}
	if _, err := dec.ReadToken(); err != nil {
		return err
	}
	v.m = m
	return nil
}

func valueMessage(err error) string {
	if se, ok := errors.AsType[*json.SemanticError](err); ok && se.GoType != nil && se.JSONKind != 0 {
		return fmt.Sprintf("cannot use a JSON %s as %s", jsonKindName(se.JSONKind), se.GoType)
	}
	return err.Error()
}

func jsonKindName(k jsontext.Kind) string {
	switch k {
	case '"':
		return "string"
	case '0':
		return "number"
	case 't', 'f':
		return "boolean"
	case '{':
		return "object"
	case '[':
		return "array"
	default:
		return "null"
	}
}

// DecodeValue is the ValueDecoder of a field whose JSON form is V's own.
func DecodeValue[V any](dec *jsontext.Decoder) (any, error) {
	if dec.PeekKind() == 'n' {
		return nil, dec.SkipValue()
	}
	var x V
	if err := json.UnmarshalDecode(dec, &x); err != nil {
		return nil, err
	}
	return x, nil
}

// DecodeJSONB is the ValueDecoder of a JSONB field: any JSON value, stored
// as its compact bytes.
func DecodeJSONB(dec *jsontext.Decoder) (any, error) {
	var raw jsontext.Value
	if err := json.UnmarshalDecode(dec, &raw); err != nil {
		return nil, err
	}
	if raw.Kind() == 'n' {
		return nil, nil
	}
	if err := raw.Compact(); err != nil {
		return nil, err
	}
	return []byte(raw), nil
}

// DecodeDate is the ValueDecoder of a Date field: a YYYY-MM-DD string or an
// RFC 3339 timestamp.
func DecodeDate(dec *jsontext.Decoder) (any, error) {
	if dec.PeekKind() == 'n' {
		return nil, dec.SkipValue()
	}
	var s string
	if err := json.UnmarshalDecode(dec, &s); err != nil {
		return nil, err
	}
	if t, err := time.Parse(time.DateOnly, s); err == nil {
		return t, nil
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return nil, errors.New("must be a YYYY-MM-DD date or an RFC 3339 timestamp")
	}
	return t, nil
}
