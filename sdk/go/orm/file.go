package orm

import (
	"encoding/json/v2"
	"time"
)

// FileRef contains read-only metadata for a tenant file. Private URLs expire
// at URLExpiresAt; public URLs have no expiry. Refresh the record to renew a URL.
type FileRef struct {
	ID           string     `json:"id"`
	Name         string     `json:"name"`
	ContentType  string     `json:"content_type"`
	SizeBytes    int64      `json:"size_bytes"`
	URL          string     `json:"url"`
	URLExpiresAt *time.Time `json:"url_expires_at"`
}

// DecodeFileRef decodes a file expansion; null denotes an unavailable file.
func DecodeFileRef(value any) (*FileRef, error) {
	if value == nil {
		return nil, nil
	}

	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}

	var ref FileRef
	if err := json.Unmarshal(data, &ref); err != nil {
		return nil, err
	}

	return new(ref), nil
}

// DecodeFileRefs decodes a multi-file expansion; null returns a nil slice.
func DecodeFileRefs(value any) ([]FileRef, error) {
	if value == nil {
		return nil, nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var refs []FileRef
	if err := json.Unmarshal(data, &refs); err != nil {
		return nil, err
	}
	return refs, nil
}
