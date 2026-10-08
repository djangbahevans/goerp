// Package storage uploads files to tenant object storage through the
// engine's host.storage calls.
package storage

import (
	"github.com/djangbahevans/goerp/sdk/go/internal/hostcall"

	abi "github.com/djangbahevans/goerp/contract/abi/v1"
)

// UploadOpts configures Upload — Public makes the file downloadable
// without an authenticated session, MaxSizeBytes overrides the module's
// default upload size limit for this call (never above the module's own
// declared ceiling), and Purpose classifies the upload (defaults to
// "attachments").
type UploadOpts = abi.StorageUploadOpts

type UploadInput = abi.StorageUploadInput

type UploadOutput = abi.StorageUploadOutput

// Upload stores a file via host.storage.upload.
func Upload(in UploadInput) (UploadOutput, error) {
	var out UploadOutput
	err := hostcall.Do(hostStorageUpload, in, &out)
	return out, err
}
