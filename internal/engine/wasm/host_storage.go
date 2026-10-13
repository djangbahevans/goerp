package wasm

import (
	"bytes"
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path"
	"time"
	"uuid"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/djangbahevans/goerp/internal/engine/files"
	"github.com/djangbahevans/goerp/internal/engine/storage"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/vmihailenco/msgpack/v5"
)

const defaultUploadPurpose = "attachments"

type storageUploadLimits struct {
	maxFileBytes int64
	allowedTypes []string
	blockedTypes []string
}

func registerHostStorage(ctx context.Context, rt wazero.Runtime, r *Runtime, backend storage.Backend, filesStore *files.Store, limits storageUploadLimits) error {
	_, err := r.guardedHostModule(rt, "host.storage").
		NewFunctionBuilder().WithFunc(makeStorageUpload(r, backend, filesStore, limits)).Export("upload").
		NewFunctionBuilder().WithFunc(makeStorageFileCall(r, backend, filesStore, "url", abi.CapStorageRead)).Export("url").
		NewFunctionBuilder().WithFunc(makeStorageFileCall(r, backend, filesStore, "metadata", abi.CapStorageRead)).Export("metadata").
		NewFunctionBuilder().WithFunc(makeStorageFileCall(r, backend, filesStore, "delete", abi.CapStorageWrite)).Export("delete").
		Instantiate(ctx)
	return err
}

func makeStorageUpload(r *Runtime, backend storage.Backend, filesStore *files.Store, limits storageUploadLimits) func(ctx context.Context, m api.Module, ptr, length uint32) uint64 {
	return func(ctx context.Context, m api.Module, ptr, length uint32) uint64 {
		inst := r.InstanceForModule(m)
		modCtx := inst.ModuleContext()
		allocate := inst.allocate

		if !modCtx.Capabilities().Has(abi.CapStorageWrite) {
			return abi.EncodeHostError(ctx, m, allocate, abi.CapabilityDenied("storage.write"))
		}

		// Storage is optional at engine startup.
		if backend == nil {
			return abi.EncodeHostError(ctx, m, allocate, &abiv1.HostError{
				Code:    "storage.backend_unavailable",
				Message: "no object storage backend is configured",
			})
		}

		inputBytes, err := abi.ReadFromModule(m.Memory(), ptr, length)
		if err != nil {
			return abi.EncodeHostError(ctx, m, allocate, abi.MemoryFault())
		}
		var input abiv1.StorageUploadInput
		if err := msgpack.Unmarshal(inputBytes, &input); err != nil {
			return abi.EncodeHostError(ctx, m, allocate, abi.DeserializeError(err))
		}

		size := int64(len(input.Data))
		if size > limits.maxFileBytes || (input.Opts.MaxSizeBytes > 0 && size > input.Opts.MaxSizeBytes) {
			return abi.EncodeHostError(ctx, m, allocate, &abiv1.HostError{
				Code:    "storage.file_too_large",
				Message: fmt.Sprintf("upload of %d bytes exceeds the maximum allowed size", size),
			})
		}

		if !storage.ContentTypeAllowed(input.ContentType, limits.allowedTypes, limits.blockedTypes) {
			return abi.EncodeHostError(ctx, m, allocate, &abiv1.HostError{
				Code:    "storage.invalid_content_type",
				Message: fmt.Sprintf("content type %q is not permitted", input.ContentType),
			})
		}

		purpose := cmp.Or(input.Opts.Purpose, defaultUploadPurpose)
		if !storage.ValidPurpose(purpose) {
			return abi.EncodeHostError(ctx, m, allocate, &abiv1.HostError{
				Code:    "storage.invalid_purpose",
				Message: fmt.Sprintf("purpose %q is not a valid storage key segment", purpose),
			})
		}

		fileID := uuid.NewV7()
		key := storage.BuildKey(purpose, modCtx.TenantID, fileID.String(), path.Ext(input.Filename))

		checksum := sha256.Sum256(input.Data)
		checksumHex := hex.EncodeToString(checksum[:])

		if _, err := backend.Upload(ctx, key, bytes.NewReader(input.Data), storage.UploadOptions{
			ContentType: input.ContentType,
			Public:      input.Opts.Public,
		}); err != nil {
			return abi.EncodeHostError(ctx, m, allocate, &abiv1.HostError{
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
			// Metadata failure must not leave an untracked object; cleanup is best effort.
			_ = backend.Delete(ctx, key)
			return abi.EncodeHostError(ctx, m, allocate, &abiv1.HostError{
				Code:    abiv1.ErrCodeUnavailable,
				Message: err.Error(),
				Retry:   true,
			})
		}

		out := abiv1.StorageUploadOutput{
			FileID:         fileID.String(),
			StorageKey:     key,
			SizeBytes:      size,
			ChecksumSHA256: checksumHex,
		}
		if input.Opts.Public {
			// A convenience URL failure does not undo a successful upload.
			if url, err := backend.PublicURL(ctx, key); err == nil {
				out.URL = url
			}
		}

		return abi.WriteToModule(ctx, m, allocate, out)
	}
}

func makeStorageFileCall(r *Runtime, backend storage.Backend, store *files.Store, operation string, capability abi.CapabilitySet) func(context.Context, api.Module, uint32, uint32) uint64 {
	return func(ctx context.Context, m api.Module, ptr, length uint32) uint64 {
		inst := r.InstanceForModule(m)
		mc := inst.ModuleContext()
		fail := func(err *abiv1.HostError) uint64 {
			return abi.EncodeHostError(ctx, m, inst.allocate, err)
		}
		if !mc.Capabilities().Has(capability) {
			name := "storage.read"
			if capability == abi.CapStorageWrite {
				name = "storage.write"
			}
			return fail(abi.CapabilityDenied(name))
		}

		data, err := abi.ReadFromModule(m.Memory(), ptr, length)
		if err != nil {
			return fail(abi.MemoryFault())
		}

		var input abiv1.StorageURLInput
		if err := msgpack.Unmarshal(data, &input); err != nil {
			return fail(abi.DeserializeError(err))
		}
		if _, err := uuid.Parse(input.FileID); err != nil {
			return fail(storageFileNotFound())
		}
		if store == nil {
			return fail(storageMetadataUnavailable())
		}

		file, err := store.GetByID(ctx, mc.TenantSlug, input.FileID)
		if errors.Is(err, files.ErrFileNotFound) {
			return fail(storageFileNotFound())
		}
		if err != nil {
			return fail(storageMetadataUnavailable())
		}
		if file.DeletedAt != nil || file.TenantID != mc.TenantID {
			return fail(storageFileNotFound())
		}

		switch operation {
		case "metadata":
			return abi.WriteToModule(ctx, m, inst.allocate, abiv1.FileMetadata{
				ID:           file.ID,
				OriginalName: file.OriginalName,
				ContentType:  file.ContentType,
				SizeBytes:    file.SizeBytes,
				Checksum:     file.ChecksumSHA256,
				IsPublic:     file.IsPublic,
				UploadedBy:   file.UploadedBy,
				Purpose:      file.Purpose,
				ScanStatus:   file.ScanStatus,
				CreatedAt:    file.CreatedAt,
			})
		case "delete":
			if err := store.DeleteActive(ctx, mc.TenantSlug, mc.TenantID, file.ID); err != nil {
				if errors.Is(err, files.ErrFileNotFound) {
					return fail(storageFileNotFound())
				}
				return fail(storageMetadataUnavailable())
			}
			return abi.WriteToModule(ctx, m, inst.allocate, nil)
		default:
			seconds := int64(3600)
			if !file.IsPublic && input.ExpiresInSeconds != nil {
				seconds = *input.ExpiresInSeconds
			}
			if !file.IsPublic && (seconds <= 0 || seconds > 86400) {
				return fail(&abiv1.HostError{Code: "storage.invalid_expiry", Message: "expiry must be between 1 and 86400 seconds"})
			}
			if backend == nil {
				return fail(&abiv1.HostError{Code: "storage.backend_unavailable", Message: "no object storage backend is configured"})
			}

			var out abiv1.StorageURLOutput
			if file.IsPublic {
				out.URL, err = backend.PublicURL(ctx, file.StorageKey)
			} else {
				expiry := time.Duration(seconds) * time.Second
				out.ExpiresAt = r.Now().Add(expiry).Unix()
				out.URL, err = backend.SignedURL(ctx, file.StorageKey, expiry)
			}
			if err != nil {
				return fail(&abiv1.HostError{Code: "storage.backend_unavailable", Message: "could not obtain file URL", Retry: true})
			}
			return abi.WriteToModule(ctx, m, inst.allocate, out)
		}
	}
}

func storageFileNotFound() *abiv1.HostError {
	return &abiv1.HostError{Code: "storage.file_not_found", Message: "file not found"}
}

func storageMetadataUnavailable() *abiv1.HostError {
	return &abiv1.HostError{Code: abiv1.ErrCodeUnavailable, Message: "file metadata is unavailable", Retry: true}
}
