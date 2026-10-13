package abi

import "time"

// StorageUploadOpts holds the options of host.storage.upload.
type StorageUploadOpts struct {
	Public       bool   `msgpack:"public"`
	MaxSizeBytes int64  `msgpack:"max_size_bytes"`
	Purpose      string `msgpack:"purpose"`
}

// StorageUploadInput is the request of host.storage.upload.
type StorageUploadInput struct {
	Filename    string            `msgpack:"filename"`
	ContentType string            `msgpack:"content_type"`
	Data        []byte            `msgpack:"data"`
	Opts        StorageUploadOpts `msgpack:"opts"`
}

// StorageUploadOutput is the response of host.storage.upload.
type StorageUploadOutput struct {
	FileID         string `msgpack:"file_id"`
	StorageKey     string `msgpack:"storage_key"`
	SizeBytes      int64  `msgpack:"size_bytes"`
	ChecksumSHA256 string `msgpack:"checksum_sha256"`
	URL            string `msgpack:"url,omitempty"`
}

// StorageFileInput identifies a file in the calling tenant.
type StorageFileInput struct {
	FileID string `msgpack:"file_id"`
}

// StorageURLInput requests a file URL. A nil expiry defaults to 3600 seconds;
// private files accept 1 through 86400 seconds, and public files ignore expiry.
type StorageURLInput struct {
	FileID           string `msgpack:"file_id"`
	ExpiresInSeconds *int64 `msgpack:"expires_in_seconds,omitempty"`
}

// StorageURLOutput carries the URL and its Unix-second expiry, or zero for public files.
type StorageURLOutput struct {
	URL       string `msgpack:"url"`
	ExpiresAt int64  `msgpack:"expires_at"`
}

// FileMetadata describes an active file in the calling tenant. UploadedBy is empty
// when the upload has no acting user; Checksum contains the hexadecimal SHA-256 digest.
type FileMetadata struct {
	ID           string    `msgpack:"id"`
	OriginalName string    `msgpack:"original_name"`
	ContentType  string    `msgpack:"content_type"`
	SizeBytes    int64     `msgpack:"size_bytes"`
	Checksum     string    `msgpack:"checksum"`
	IsPublic     bool      `msgpack:"is_public"`
	UploadedBy   string    `msgpack:"uploaded_by"`
	Purpose      string    `msgpack:"purpose"`
	ScanStatus   string    `msgpack:"scan_status"`
	CreatedAt    time.Time `msgpack:"created_at"`
}
