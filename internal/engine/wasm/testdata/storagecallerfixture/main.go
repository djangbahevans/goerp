// Command storagecallerfixture is a real Go module compiled to wasip1
// WASM for internal/engine/wasm's own host.storage module-side caller
// test (goerp#434) — it calls host.storage.upload through the real
// sdk/go/storage package, rather than a hand-assembled bytecode
// stand-in.
//
// Must be built with:
//
//	GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o storagecallerfixture.wasm .
package main

import (
	"github.com/djangbahevans/goerp/sdk/go/engine"
	"github.com/djangbahevans/goerp/sdk/go/storage"
	"github.com/vmihailenco/msgpack/v5"
)

type result struct {
	OK             bool   `msgpack:"ok"`
	Error          string `msgpack:"error,omitempty"`
	FileID         string `msgpack:"file_id,omitempty"`
	StorageKey     string `msgpack:"storage_key,omitempty"`
	SizeBytes      int64  `msgpack:"size_bytes,omitempty"`
	ChecksumSHA256 string `msgpack:"checksum_sha256,omitempty"`
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
