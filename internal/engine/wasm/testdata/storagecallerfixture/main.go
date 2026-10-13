// Command storagecallerfixture exercises the storage SDK in a compiled WASI module.
package main

import (
	"errors"
	"time"

	abi "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/sdk/go/engine"
	"github.com/djangbahevans/goerp/sdk/go/storage"
	"github.com/vmihailenco/msgpack/v5"
)

type result struct {
	Metadata       storage.FileMetadata `msgpack:"metadata"`
	URL            string               `msgpack:"url"`
	PublicURL      string               `msgpack:"public_url"`
	OK             bool                 `msgpack:"ok"`
	Error          string               `msgpack:"error,omitempty"`
	FileID         string               `msgpack:"file_id,omitempty"`
	StorageKey     string               `msgpack:"storage_key,omitempty"`
	SizeBytes      int64                `msgpack:"size_bytes,omitempty"`
	ChecksumSHA256 string               `msgpack:"checksum_sha256,omitempty"`
}

func writeResult(r result) uint64 {
	data, err := msgpack.Marshal(r)
	if err != nil {
		data, _ = msgpack.Marshal(result{Error: "marshal result: " + err.Error()})
	}
	ptr := engine.Allocate(uint32(len(data)))
	engine.WriteMem(ptr, data)
	return uint64(ptr)<<32 | uint64(len(data))
}

//go:wasmexport run_upload
func runUpload() uint64 {
	out, err := storage.Upload(storage.UploadInput{
		Filename:    "hello.txt",
		ContentType: "text/plain",
		Data:        []byte("hello from a real wasip1 module"),
		Opts:        storage.UploadOpts{Purpose: "attachments"},
	})
	if err != nil {
		return writeResult(result{Error: err.Error()})
	}
	return writeResult(result{
		OK:             true,
		FileID:         out.FileID,
		StorageKey:     out.StorageKey,
		SizeBytes:      out.SizeBytes,
		ChecksumSHA256: out.ChecksumSHA256,
	})
}

//go:wasmexport allocate
func allocate(size uint32) uint32 {
	return engine.Allocate(size)
}

//go:wasmexport deallocate
func deallocate(ptr, size uint32) {
	engine.Deallocate(ptr, size)
}

func main() {}

//go:wasmexport run_lifecycle
func runLifecycle() uint64 {
	upload, err := storage.Upload(storage.UploadInput{
		Filename:    "private.txt",
		ContentType: "text/plain",
		Data:        []byte("private file"),
	})
	if err != nil {
		return writeResult(result{Error: err.Error()})
	}
	metadata, err := storage.Metadata(upload.FileID)
	if err != nil {
		return writeResult(result{Error: err.Error()})
	}
	for _, expiry := range []time.Duration{-time.Nanosecond, 24*time.Hour + time.Nanosecond} {
		if _, err := storage.SignedURL(upload.FileID, expiry); !hasCode(err, "storage.invalid_expiry") {
			return writeResult(result{Error: "SignedURL must reject invalid private expiry"})
		}
	}
	url, err := storage.SignedURL(upload.FileID, time.Minute)
	if err != nil {
		return writeResult(result{Error: err.Error()})
	}
	if _, err := storage.PublicURL(upload.FileID); !hasCode(err, "storage.file_not_public") {
		return writeResult(result{Error: "PublicURL must reject private files"})
	}
	public, err := storage.Upload(storage.UploadInput{
		Filename:    "public.txt",
		ContentType: "text/plain",
		Data:        []byte("public file"),
		Opts:        storage.UploadOpts{Public: true},
	})
	if err != nil {
		return writeResult(result{Error: err.Error()})
	}
	publicURL, err := storage.PublicURL(public.FileID)
	if err != nil {
		return writeResult(result{Error: err.Error()})
	}
	again, err := storage.SignedURL(public.FileID, 48*time.Hour)
	if err != nil || publicURL != again {
		return writeResult(result{Error: "public URL must be stable"})
	}
	if err := storage.Delete(upload.FileID); err != nil {
		return writeResult(result{Error: err.Error()})
	}
	if _, err := storage.Metadata(upload.FileID); !hasCode(err, "storage.file_not_found") {
		return writeResult(result{Error: "Metadata must reject deleted files"})
	}
	if _, err := storage.SignedURL(upload.FileID, 0); !hasCode(err, "storage.file_not_found") {
		return writeResult(result{Error: "SignedURL must reject deleted files"})
	}
	if err := storage.Delete(upload.FileID); !hasCode(err, "storage.file_not_found") {
		return writeResult(result{Error: "Delete must reject deleted files"})
	}
	return writeResult(result{OK: true, FileID: upload.FileID, StorageKey: upload.StorageKey, Metadata: metadata, URL: url, PublicURL: publicURL})
}

func hasCode(err error, code string) bool {
	hostErr, ok := errors.AsType[*abi.HostError](err)
	return ok && hostErr.Code == code
}
