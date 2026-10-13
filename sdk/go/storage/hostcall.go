// Package storage manages tenant files through object storage host calls.
package storage

import (
	"time"

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

// FileMetadata describes an active tenant file, including its SHA-256 checksum,
// scan status and creation time. UploadedBy is empty for anonymous uploads.
type FileMetadata = abi.FileMetadata

// SignedURL returns a file URL. Zero expiry defaults to one hour; private files
// accept one second through 24 hours. Positive durations round up to whole seconds.
// Public files return their stable URL and ignore expiry.
func SignedURL(fileID string, expiry time.Duration) (string, error) {
	input := abi.StorageURLInput{FileID: fileID}
	if expiry != 0 {
		seconds := int64(expiry / time.Second)
		if expiry%time.Second != 0 {
			if expiry > 0 {
				seconds++
			} else {
				seconds--
			}
		}
		input.ExpiresInSeconds = new(seconds)
	}

	var out abi.StorageURLOutput
	err := hostcall.Do(hostStorageURL, input, &out)
	return out.URL, err
}

// PublicURL returns a stable URL for a public file. Private files return
// storage.file_not_public; missing or deleted files return storage.file_not_found.
func PublicURL(fileID string) (string, error) {
	metadata, err := Metadata(fileID)
	if err != nil {
		return "", err
	}
	if !metadata.IsPublic {
		return "", &abi.HostError{Code: "storage.file_not_public", Message: "file is not public"}
	}

	return SignedURL(fileID, 0)
}

// Metadata returns every metadata field for an active file in the calling tenant.
// Missing, cross-tenant and deleted files return storage.file_not_found.
func Metadata(fileID string) (FileMetadata, error) {
	var out FileMetadata
	err := hostcall.Do(hostStorageMetadata, abi.StorageFileInput{FileID: fileID}, &out)
	return out, err
}

// Delete soft-deletes an active file in the calling tenant without removing its
// backend object. Missing, cross-tenant and deleted files return storage.file_not_found.
func Delete(fileID string) error {
	return hostcall.Do(hostStorageDelete, abi.StorageFileInput{FileID: fileID}, nil)
}
