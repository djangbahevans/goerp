package engine

import (
	"encoding/base64"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"maps"
	"time"

	"github.com/djangbahevans/goerp/sdk/go/model"
)

// ormRecordToJSON converts a record the ORM returned into each field
// kind's JSON shape (typescript-sdk-reference.md §4 "Types"): a jsonb
// field as the stored JSON value rather than base64 of its bytes, a date
// field as "YYYY-MM-DD", and a timestamptz field in UTC whatever the
// server's zone. A bytea field's bytes already encode as base64.
func ormRecordToJSON(md model.ModelDeclaration, record map[string]any) map[string]any {
	if record == nil {
		return nil
	}
	out := maps.Clone(record)
	for _, f := range md.Fields {
		switch v := out[f.Name].(type) {
		case []byte:
			if f.Def.Kind == model.KindJSONB {
				out[f.Name] = jsontext.Value(v)
			}
		case string:
			if f.Def.Kind == model.KindJSONB {
				out[f.Name] = jsontext.Value(v)
			}
		case time.Time:
			switch f.Def.Kind {
			case model.KindDate:
				out[f.Name] = v.Format(time.DateOnly)
			case model.KindTimestampTZ:
				out[f.Name] = v.UTC()
			}
		}
	}
	return out
}

func ormRecordsToJSON(md model.ModelDeclaration, records []map[string]any) []map[string]any {
	out := make([]map[string]any, len(records))
	for i, r := range records {
		out[i] = ormRecordToJSON(md, r)
	}
	return out
}

// ormRecordFromJSON converts a JSON request body into the values the ORM
// writes: a bytea field's base64 string into its bytes, and a jsonb
// field's value into JSON bytes, the form module code also reads and
// writes it in, so a bare JSON string or number stores as that JSON value. It names the field whose value can't be converted.
func ormRecordFromJSON(md model.ModelDeclaration, record map[string]any) (map[string]any, error) {
	out := maps.Clone(record)
	for _, f := range md.Fields {
		v, ok := out[f.Name]
		if !ok || v == nil {
			continue
		}
		switch f.Def.Kind {
		case model.KindBytea:
			s, isString := v.(string)
			if !isString {
				return nil, fmt.Errorf("field %s must be a base64 string", f.Name)
			}
			decoded, err := base64.StdEncoding.DecodeString(s)
			if err != nil {
				return nil, fmt.Errorf("field %s must be a base64 string: %w", f.Name, err)
			}
			out[f.Name] = decoded
		case model.KindJSONB:
			encoded, err := json.Marshal(v)
			if err != nil {
				return nil, fmt.Errorf("field %s: %w", f.Name, err)
			}
			out[f.Name] = encoded
		}
	}
	return out, nil
}
