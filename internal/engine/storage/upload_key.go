package storage

import (
	"fmt"
	"regexp"
	"time"
)

// validPurposePattern bounds purpose to characters safe as a raw path
// segment — object-storage-guide.md §12 documents "attachments",
// "avatars", and "imports" as standard values, but doesn't enforce an
// enum, so any caller-supplied string in this shape is accepted.
var validPurposePattern = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)

// ValidPurpose reports whether purpose is safe to use as BuildKey's first
// path segment. Both upload paths (host.storage.upload,
// POST /storage/upload) must reject an invalid purpose before calling
// BuildKey — an unchecked purpose containing "/" or ".." lets a caller
// escape the intended {purpose}/{tenant_id}/... prefix, and on
// LocalBackend, filepath.Join resolves ".." segments with no sandboxing
// to the storage directory.
func ValidPurpose(purpose string) bool {
	return validPurposePattern.MatchString(purpose)
}

// BuildKey returns the {purpose}/{tenant_id}/{year}/{month}/{file_id}{ext}
// object key (object-storage-guide.md §12) — purpose first so a single S3
// prefix filter matches every tenant's files under that purpose. Shared by
// both host.storage.upload and POST /storage/upload so the two upload
// paths can never drift on key layout. Callers must validate purpose with
// ValidPurpose first — BuildKey itself doesn't re-check it.
func BuildKey(purpose, tenantID, fileID, ext string) string {
	return fmt.Sprintf("%s/%s/%s/%s%s", purpose, tenantID, time.Now().UTC().Format("2006/01"), fileID, ext)
}
