package notify

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"strconv"

	"github.com/vmihailenco/msgpack/v5"
)

// encodeData encodes data the way encoding/json/v2 does, so a template
// variable is a field's json tag name or its Go name when untagged, and
// returns it as the msgpack map host.notify takes. An integer stays an
// integer rather than becoming a float a template would print as 1e+06.
func encodeData(data any) ([]byte, error) {
	encoded, err := json.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("encode data: %w", err)
	}
	dec := jsontext.NewDecoder(bytes.NewReader(encoded))
	value, err := decodeValue(dec)
	if err != nil {
		return nil, fmt.Errorf("encode data: %w", err)
	}
	if _, ok := value.(map[string]any); !ok {
		return nil, errors.New("data must encode as a JSON object: use a struct or a map")
	}
	return msgpack.Marshal(value)
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
