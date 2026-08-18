package wasm

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/djangbahevans/goerp/internal/engine/files"
	"github.com/djangbahevans/goerp/internal/engine/storage"
	"github.com/google/uuid"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/vmihailenco/msgpack/v5"
)

// defaultUploadPurpose is host.storage.upload's opts.purpose default
// (object-storage-guide.md §12 "Standard purposes" — the ABI-level input
// shown in host-abi-reference.md §9 omits purpose entirely even though
// files.purpose is NOT NULL; this default is what backs it).
const defaultUploadPurpose = "attachments"

// storageUploadLimits carries the config.Config fields makeStorageUpload
// needs, so the closure doesn't have to import config for one struct.
type storageUploadLimits struct {
	maxFileBytes int64
	allowedTypes []string
	blockedTypes []string
}

// registerHostStorage attaches host.storage.upload to the runtime. Lives
// in the wasm package for the same import-cycle reason registerHostDB
// does (host_db.go) — its closure needs direct access to the Runtime's
// instance registry, storage.Backend, and *files.Store.
func registerHostStorage(ctx context.Context, rt wazero.Runtime, r *Runtime, backend storage.Backend, filesStore *files.Store, limits storageUploadLimits) error {
	_, err := rt.NewHostModuleBuilder("host.storage").
		NewFunctionBuilder().WithFunc(makeStorageUpload(r, backend, filesStore, limits)).Export("upload").
		Instantiate(ctx)
	return err
}

type storageUploadOpts struct {
	Public       bool   `msgpack:"public"`
	MaxSizeBytes int64  `msgpack:"max_size_bytes"`
	Purpose      string `msgpack:"purpose"`
}

type storageUploadInput struct {
	Filename    string            `msgpack:"filename"`
	ContentType string            `msgpack:"content_type"`
	Data        []byte            `msgpack:"data"`
	Opts        storageUploadOpts `msgpack:"opts"`
}

type storageUploadOutput struct {
	FileID         string `msgpack:"file_id"`
	StorageKey     string `msgpack:"storage_key"`
	SizeBytes      int64  `msgpack:"size_bytes"`
	ChecksumSHA256 string `msgpack:"checksum_sha256"`
	URL            string `msgpack:"url,omitempty"`
}

func makeStorageUpload(r *Runtime, backend storage.Backend, filesStore *files.Store, limits storageUploadLimits) func(ctx context.Context, m api.Module, ptr, length uint32) uint64 {
	return func(ctx context.Context, m api.Module, ptr, length uint32) uint64 {
		inst := r.InstanceForModule(m)
		modCtx := inst.ModuleContext()
		allocate := inst.allocate

		if !modCtx.Capabilities().Has(abi.CapStorageWrite) {
			return abi.EncodeHostError(ctx, m, allocate, abi.CapabilityDenied("storage.write"))
		}

		// storage.New (engine.go) is a warn-only dependency — a fully
		// successful Engine.New() can still leave this nil.
		if backend == nil {
			return abi.EncodeHostError(ctx, m, allocate, &abi.HostError{
				Code:    "storage.backend_unavailable",
				Message: "no object storage backend is configured",
			})
		}

		inputBytes, err := abi.ReadFromModule(m.Memory(), ptr, length)
		if err != nil {
			return abi.EncodeHostError(ctx, m, allocate, abi.MemoryFault())
		}
		var input storageUploadInput
		if err := msgpack.Unmarshal(inputBytes, &input); err != nil {
			return abi.EncodeHostError(ctx, m, allocate, abi.DeserializeError(err))
		}

		size := int64(len(input.Data))
		if size > limits.maxFileBytes || (input.Opts.MaxSizeBytes > 0 && size > input.Opts.MaxSizeBytes) {
			return abi.EncodeHostError(ctx, m, allocate, &abi.HostError{
				Code:    "storage.file_too_large",
				Message: fmt.Sprintf("upload of %d bytes exceeds the maximum allowed size", size),
			})
		}

		if !contentTypeAllowed(input.ContentType, limits) {
			return abi.EncodeHostError(ctx, m, allocate, &abi.HostError{
				Code:    "storage.invalid_content_type",
				Message: fmt.Sprintf("content type %q is not permitted", input.ContentType),
			})
		}

		purpose := input.Opts.Purpose
		if purpose == "" {
			purpose = defaultUploadPurpose
		}

		fileID, err := uuid.NewV7()
		if err != nil {
			return abi.EncodeHostError(ctx, m, allocate, &abi.HostError{Code: abi.ErrCodeUnavailable, Message: err.Error()})
		}

		// {purpose}/{tenant_id}/{year}/{month}/{file_id}.{ext}
		// (object-storage-guide.md §12) — purpose first so a single S3
		// prefix filter matches every tenant's files under that purpose.
		key := fmt.Sprintf("%s/%s/%s/%s%s",
			purpose, modCtx.TenantID, time.Now().UTC().Format("2006/01"), fileID.String(), path.Ext(input.Filename))

		checksum := sha256.Sum256(input.Data)
		checksumHex := hex.EncodeToString(checksum[:])

		if _, err := backend.Upload(ctx, key, bytes.NewReader(input.Data), storage.UploadOptions{
			ContentType: input.ContentType,
			Public:      input.Opts.Public,
		}); err != nil {
			return abi.EncodeHostError(ctx, m, allocate, &abi.HostError{
				Code:    "storage.backend_unavailable",
				Message: err.Error(),
				Retry:   true,
			})
		}

		if err := filesStore.Insert(ctx, modCtx.TenantSlug, files.InsertRow{
			ID:             fileID.String(),
			TenantID:       modCtx.TenantID,
			StorageKey:     key,
			OriginalName:   input.Filename,
			ContentType:    input.ContentType,
			SizeBytes:      size,
			ChecksumSHA256: checksumHex,
			UploadedBy:     modCtx.UserID,
			Purpose:        purpose,
			IsPublic:       input.Opts.Public,
		}); err != nil {
			// Best-effort cleanup so a metadata-write failure doesn't
			// leave an orphaned object with no files row pointing at it.
			// The upload itself already succeeded, so the delete error
			// (if any) isn't the primary failure to report.
			_ = backend.Delete(ctx, key)
			return abi.EncodeHostError(ctx, m, allocate, &abi.HostError{
				Code:    abi.ErrCodeUnavailable,
				Message: err.Error(),
				Retry:   true,
			})
		}

		out := storageUploadOutput{
			FileID:         fileID.String(),
			StorageKey:     key,
			SizeBytes:      size,
			ChecksumSHA256: checksumHex,
		}
		if input.Opts.Public {
			// Best-effort: the file is already stored and recorded, so a
			// PublicURL failure only omits the convenience URL, not the
			// upload's success.
			if url, err := backend.PublicURL(ctx, key); err == nil {
				out.URL = url
			}
		}

		return abi.WriteToModule(ctx, m, allocate, out)
	}
}

func contentTypeAllowed(contentType string, limits storageUploadLimits) bool {
	ct := strings.ToLower(contentType)

	for _, blocked := range limits.blockedTypes {
		if strings.ToLower(blocked) == ct {
			return false
		}
	}

	if len(limits.allowedTypes) == 0 {
		return true
	}
	for _, allowed := range limits.allowedTypes {
		if strings.ToLower(allowed) == ct {
			return true
		}
	}
	return false
}
