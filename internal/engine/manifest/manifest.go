// Package manifest loads and validates a module's manifest.json:
// LoadManifest guards encoding (UTF-8, no comments — JSON syntax rejects
// those for free — and the 1MB size cap, manifest-spec.md §1), and Manifest
// is the typed struct every root field (manifest-spec.md §2) decodes into.
// Loading a manifest out of a real .erp package, as opposed to a loose
// manifest.json fixture, is out of scope here — see #13.
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
