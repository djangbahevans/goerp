package module

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"os"
	"unicode/utf8"

	"github.com/djangbahevans/goerp/internal/engine/manifest"
)

// readManifestJSON reads manifestPath and decodes it into a generic
// key/value map, rejecting invalid UTF-8 and duplicate object member names
// with the same strictness manifest.Load applies when the engine loads a
// module — a manifest that build/package accepted but the engine later
// rejected used to only surface that failure far from its cause.
func readManifestJSON(manifestPath string) (map[string]any, error) {
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("read manifest: %w", err)
	}

	if !utf8.Valid(raw) {
		return nil, fmt.Errorf("manifest %s: %w", manifestPath, manifest.ErrInvalidUtf8)
	}
	// jsontext.Value.IsValid, unlike v1's json.Valid, also rejects a
	// duplicate object member name.
	if !jsontext.Value(raw).IsValid() {
		return nil, fmt.Errorf("manifest %s: %w", manifestPath, manifest.ErrInvalidJSON)
	}

	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, fmt.Errorf("parse manifest %s: %w", manifestPath, err)
	}

	return decoded, nil
}
