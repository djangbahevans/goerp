// Package storage is sdk/go's outbound module-side caller for the
// host.storage namespace (host-abi-reference.md §9) — currently just
// Upload, calling host.storage.upload via sdk/go/internal/hostcall.
// host.storage.url/delete/metadata are documented but not implemented
// engine-side yet, so this package doesn't offer them either.
package storage

import "github.com/djangbahevans/goerp/sdk/go/internal/hostcall"

// UploadOpts configures Upload — Public makes the file downloadable
// without an authenticated session, MaxSizeBytes overrides the module's
// default upload size limit for this call (never above the module's own
// declared ceiling), and Purpose classifies the upload (defaults to
// "attachments" if left empty — object-storage-guide.md §12 "Standard
// purposes").
type UploadOpts struct {
	Public       bool   `msgpack:"public"`
	MaxSizeBytes int64  `msgpack:"max_size_bytes"`
	Purpose      string `msgpack:"purpose"`
}

type UploadInput struct {
	Filename    string     `msgpack:"filename"`
	ContentType string     `msgpack:"content_type"`
	Data        []byte     `msgpack:"data"`
	Opts        UploadOpts `msgpack:"opts"`
}

type UploadOutput struct {
	FileID         string `msgpack:"file_id"`
	StorageKey     string `msgpack:"storage_key"`
	SizeBytes      int64  `msgpack:"size_bytes"`
	ChecksumSHA256 string `msgpack:"checksum_sha256"`
	URL            string `msgpack:"url,omitempty"`
}

// Upload stores a file via host.storage.upload.
func Upload(in UploadInput) (UploadOutput, error) {
	var out UploadOutput
	err := hostcall.Do(hostStorageUpload, in, &out)
	return out, err
}
