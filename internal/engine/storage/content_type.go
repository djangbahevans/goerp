package storage

import "strings"

// ContentTypeAllowed checks contentType against blocked first (always
// wins, even if also present in allowed), then allowed — an empty allowed
// list means "everything not blocked is allowed." Shared by every upload
// path (host.storage.upload, the engine's built-in POST /storage/upload)
// so the two never drift.
func ContentTypeAllowed(contentType string, allowed, blocked []string) bool {
	ct := strings.ToLower(contentType)

	for _, b := range blocked {
		if strings.ToLower(b) == ct {
			return false
		}
	}

	if len(allowed) == 0 {
		return true
	}
	for _, a := range allowed {
		if strings.ToLower(a) == ct {
			return true
		}
	}
	return false
}
