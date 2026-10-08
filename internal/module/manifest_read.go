package module

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"os"
	"unicode/utf8"

	"github.com/djangbahevans/goerp/internal/engine/manifest"
)

// readManifestJSON rejects invalid UTF-8 and duplicate members with the engine loader's
// strictness, surfacing invalid manifests before packaging.
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
