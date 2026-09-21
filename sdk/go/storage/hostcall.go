// Package storage is sdk/go's outbound module-side caller for the
// host.storage namespace (host-abi-reference.md §9) — currently just
// Upload, calling host.storage.upload via sdk/go/internal/hostcall.
// host.storage.url/delete/metadata are documented but not implemented
// engine-side yet, so this package doesn't offer them either.
package storage

import (
	"github.com/djangbahevans/goerp/sdk/go/internal/hostcall"

	abi "github.com/djangbahevans/goerp/contract/abi/v1"
)

// UploadOpts configures Upload — Public makes the file downloadable
// without an authenticated session, MaxSizeBytes overrides the module's
// default upload size limit for this call (never above the module's own
// declared ceiling), and Purpose classifies the upload (defaults to
// "attachments" if left empty — object-storage-guide.md §12 "Standard
// purposes").
type UploadOpts = abi.StorageUploadOpts

type UploadInput = abi.StorageUploadInput

type UploadOutput = abi.StorageUploadOutput

// Upload stores a file via host.storage.upload.
func Upload(in UploadInput) (UploadOutput, error) {
	var out UploadOutput
	err := hostcall.Do(hostStorageUpload, in, &out)
	return out, err
}
