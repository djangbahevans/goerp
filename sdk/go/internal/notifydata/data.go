// Package notifydata encodes template data with JSON field names and preserves integer values.
package notifydata

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"strconv"
)

// Values requires data to encode as a JSON object. Integer fields remain integers
// so templates retain their precision and decimal formatting.
func Values(data any) (map[string]any, error) {
	encoded, err := json.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("encode data: %w", err)
	}
	dec := jsontext.NewDecoder(bytes.NewReader(encoded))
	value, err := decodeValue(dec)
	if err != nil {
		return nil, fmt.Errorf("encode data: %w", err)
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil, errors.New("data must encode as a JSON object: use a struct or a map")
	}
	return object, nil
}

func decodeValue(dec *jsontext.Decoder) (any, error) {
	tok, err := dec.ReadToken()
	if err != nil {
		return nil, err
	}
	switch tok.Kind() {
	case 'n':
		return nil, nil
	case 't', 'f':
		return tok.Bool(), nil
	case '"':
		return tok.String(), nil
	case '0':
		if n, err := strconv.ParseInt(tok.String(), 10, 64); err == nil {
			return n, nil
		}
		return strconv.ParseFloat(tok.String(), 64)
	case '{':
		object := map[string]any{}
		for dec.PeekKind() != '}' {
			name, err := dec.ReadToken()
			if err != nil {
				return nil, err
			}
			if object[name.String()], err = decodeValue(dec); err != nil {
				return nil, err
			}
		}
		_, err := dec.ReadToken()
		return object, err
	case '[':
		list := []any{}
		for dec.PeekKind() != ']' {
			item, err := decodeValue(dec)
			if err != nil {
				return nil, err
			}
			list = append(list, item)
		}
		_, err := dec.ReadToken()
		return list, err
	}
	return nil, fmt.Errorf("unexpected JSON token %q", tok.String())
}
