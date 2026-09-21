package abi

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
