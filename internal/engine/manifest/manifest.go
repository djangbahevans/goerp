package manifest

import (
	"encoding/json"
	"errors"
	"unicode/utf8"
)

var (
	ErrInvalidUtf8      = errors.New("not valid utf-8")
	ErrInvalidJSON      = errors.New("not valid json")
	ErrManifestTooLarge = errors.New("manifest over 1mb size limit")
)

const (
	_  = iota
	KB = 1 << (10 * iota)
	MB
)

func LoadManifest(m []byte) error {
	if ok := utf8.Valid(m); !ok {
		return ErrInvalidUtf8
	}

	if ok := json.Valid(m); !ok {
		return ErrInvalidJSON
	}

	if len(m) >= MB {
		return ErrManifestTooLarge
	}

	return nil
}
