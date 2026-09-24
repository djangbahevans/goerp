// Package storageupload implements POST /storage/upload
// (object-storage-guide.md §4) — the engine built-in, browser-facing
// counterpart to host.storage.upload (internal/engine/wasm/host_storage.go,
// the module-side WASM host function). Same auth wiring as authme.Handler
// (tenantresolve.Resolver.ResolveByHost, then authcheck.Checker — the
// "call the primitive directly, skip the not-yet-built generic
// middleware" pattern, goerp#91) and the same files-table/storage-key
// convention as host.storage.upload, so both upload paths write into the
// same tenant's files table with no schema divergence.
package storageupload

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"uuid"

	"github.com/djangbahevans/goerp/internal/engine/auth/authcheck"
	"github.com/djangbahevans/goerp/internal/engine/auth/loginsession"
	"github.com/djangbahevans/goerp/internal/engine/files"
	"github.com/djangbahevans/goerp/internal/engine/storage"
	tenantresolve "github.com/djangbahevans/goerp/internal/engine/tenant/resolve"
)

// defaultPurpose matches host.storage.upload's own default
// (object-storage-guide.md §12 "Standard purposes"). The endpoint accepts
// any purpose string, same as host.storage.upload — "attachments" |
// "avatars" | "imports" are the documented standard values, not an
// enforced enum; nothing in object-storage-guide.md §4 specifies a
// rejection response for an unlisted purpose.
const defaultPurpose = "attachments"

// multipartOverheadBytes is the extra room MaxBytesReader allows beyond
// Limits.MaxFileBytes for multipart boundaries/headers and the small
// "purpose" field — generous, not a tight budget.
const multipartOverheadBytes = 64 << 10

// multipartMemoryBytes is ParseMultipartForm's in-memory threshold before
// a part spills to a temp file — matches net/http's own default. Purpose
// must be known (it picks the storage key's first path segment) before
// the file can be streamed to the backend, and the real client
// (file-field-upload.ts) appends "file" before "purpose" in the
// multipart body — ParseMultipartForm (buffer-then-random-access) is used
// instead of a single-pass multipart.Reader specifically so field order
// on the wire doesn't matter.
const multipartMemoryBytes = 32 << 20

type Limits struct {
	MaxFileBytes int64
	AllowedTypes []string
	BlockedTypes []string
}

type Handler struct {
	tenants *tenantresolve.Resolver
	auth    *authcheck.Checker
	backend storage.Backend
	files   *files.Store
	limits  Limits
}

func NewHandler(tenants *tenantresolve.Resolver, auth *authcheck.Checker, backend storage.Backend, filesStore *files.Store, limits Limits) *Handler {
	return &Handler{tenants: tenants, auth: auth, backend: backend, files: filesStore, limits: limits}
}

// writeJSON matches encoding/json v1's Encoder defaults, which
// json.MarshalWrite doesn't apply on its own: '<', '>', '&' escaped for
// safe HTML embedding, and U+2028/U+2029 escaped for safe JS embedding.
func writeJSON(w http.ResponseWriter, v any) {
	_ = json.MarshalWrite(w, v, jsontext.EscapeForHTML(true), jsontext.EscapeForJS(true))
}

func writeJSONError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	writeJSON(w, map[string]any{
		"error": map[string]string{"code": code, "message": message},
	})
}

func writeUnauthenticated(w http.ResponseWriter) {
	writeJSONError(w, http.StatusUnauthorized, "unauthenticated", "a valid access token is required")
}

type uploadResponse struct {
	FileID      string `json:"file_id"`
	Name        string `json:"name"`
	ContentType string `json:"content_type"`
	SizeBytes   int64  `json:"size_bytes"`
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	tenantCtx, err := h.tenants.ResolveByHost(ctx, r.Host)
	if err != nil {
		switch {
		case errors.Is(err, tenantresolve.ErrTenantNotFound):
			writeJSONError(w, http.StatusNotFound, "not_found", "not found")
		case errors.Is(err, tenantresolve.ErrTenantSuspended):
			writeJSONError(w, http.StatusForbidden, "tenant_suspended", "tenant suspended")
		case errors.Is(err, tenantresolve.ErrTenantOffboarding):
			writeJSONError(w, http.StatusForbidden, "tenant_offboarding", "tenant offboarding")
		default:
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "upload failed")
		}
		return
	}

	rawToken := authcheck.ExtractToken(r)
	if rawToken == "" {
		writeUnauthenticated(w)
		return
	}
	authCtx, err := h.auth.Authenticate(ctx, rawToken, tenantCtx.TenantID, tenantCtx.Slug, loginsession.ClientIP(r), nil, nil)
	if err != nil || !authCtx.IsAuthenticated {
		writeUnauthenticated(w)
		return
	}

	// storage.New (engine.go) is a warn-only dependency — a fully
	// successful Engine.New() can still leave this nil, same guard
	// host.storage.upload applies.
	if h.backend == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "storage_unavailable", "no object storage backend is configured")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, h.limits.MaxFileBytes+multipartOverheadBytes)
	if err := r.ParseMultipartForm(multipartMemoryBytes); err != nil {
		if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
			writeJSONError(w, http.StatusRequestEntityTooLarge, "file_too_large", "upload exceeds the maximum allowed size")
			return
		}
		writeJSONError(w, http.StatusBadRequest, "invalid_request", "malformed multipart/form-data body")
		return
	}
	defer func() { _ = r.MultipartForm.RemoveAll() }()

	fileHeaders := r.MultipartForm.File["file"]
	if len(fileHeaders) == 0 {
		writeJSONError(w, http.StatusBadRequest, "invalid_request", `"file" field is required`)
		return
	}
	fileHeader := fileHeaders[0]

	if fileHeader.Size > h.limits.MaxFileBytes {
		writeJSONError(w, http.StatusRequestEntityTooLarge, "file_too_large", "upload exceeds the maximum allowed size")
		return
	}

	contentType := fileHeader.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	if !storage.ContentTypeAllowed(contentType, h.limits.AllowedTypes, h.limits.BlockedTypes) {
		writeJSONError(w, http.StatusUnsupportedMediaType, "invalid_content_type", fmt.Sprintf("content type %q is not permitted", contentType))
		return
	}

	purpose := r.FormValue("purpose")
	if purpose == "" {
		purpose = defaultPurpose
	}
	if !storage.ValidPurpose(purpose) {
		writeJSONError(w, http.StatusBadRequest, "invalid_request", fmt.Sprintf("purpose %q is not a valid storage key segment", purpose))
		return
	}

	fileID := uuid.NewV7()
	key := storage.BuildKey(purpose, tenantCtx.TenantID, fileID.String(), path.Ext(fileHeader.Filename))

	f, err := fileHeader.Open()
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid_request", "could not read uploaded file")
		return
	}
	defer func() { _ = f.Close() }()

	// Uploaded private by default (object-storage-guide.md §6) — a
	// consumer resolves file_id to a signed URL later; this endpoint
	// never returns one itself.
	hasher := sha256.New()
	if _, err := h.backend.Upload(ctx, key, io.TeeReader(f, hasher), storage.UploadOptions{ContentType: contentType}); err != nil {
		writeJSONError(w, http.StatusServiceUnavailable, "storage_unavailable", "upload failed")
		return
	}
	checksumHex := hex.EncodeToString(hasher.Sum(nil))

	if err := h.files.Insert(ctx, tenantCtx.Slug, files.InsertRow{
		ID:             fileID.String(),
		TenantID:       tenantCtx.TenantID,
		StorageKey:     key,
		OriginalName:   fileHeader.Filename,
		ContentType:    contentType,
		SizeBytes:      fileHeader.Size,
		ChecksumSHA256: checksumHex,
		UploadedBy:     authCtx.UserID,
		Purpose:        purpose,
	}); err != nil {
		// Best-effort cleanup so a metadata-write failure doesn't leave an
		// orphaned object with no files row pointing at it — same pattern
		// host.storage.upload uses.
		_ = h.backend.Delete(ctx, key)
		writeJSONError(w, http.StatusInternalServerError, "internal_error", "upload failed")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, uploadResponse{
		FileID:      fileID.String(),
		Name:        fileHeader.Filename,
		ContentType: contentType,
		SizeBytes:   fileHeader.Size,
	})
}
