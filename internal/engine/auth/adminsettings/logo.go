package adminsettings

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"path"
	"strings"
	"uuid"

	"github.com/rs/zerolog/log"

	"github.com/djangbahevans/goerp/internal/engine/files"
	"github.com/djangbahevans/goerp/internal/engine/httperr"
	"github.com/djangbahevans/goerp/internal/engine/storage"
)

// logoPurpose is the storage key's first segment for tenant logos
// (object-storage-guide.md §12).
const logoPurpose = "logos"

// logoTypes are the image types a logo may be, keyed by the content type
// net/http sniffs from the file itself, with the extension stored under.
// SVG is left out: served from the public bucket it could carry script.
var logoTypes = map[string]string{
	"image/png":  ".png",
	"image/jpeg": ".jpg",
	"image/gif":  ".gif",
	"image/webp": ".webp",
}

// MaxLogoBytes caps a logo upload below the general storage file limit.
const MaxLogoBytes = 2 << 20

// multipartOverheadBytes is the room MaxBytesReader allows beyond the
// file for multipart boundaries and headers.
const multipartOverheadBytes = 64 << 10

type logoResponse struct {
	LogoURL *string `json:"logo_url"`
}

// ServeUploadLogo is POST /admin/settings/logo: a multipart/form-data
// "file" image, stored public (object-storage-guide.md §6, "for logos")
// and set as the tenant's logo_url. The replaced logo is retired.
func (h *Handler) ServeUploadLogo(w http.ResponseWriter, r *http.Request) {
	c, ok := h.authorize(w, r)
	if !ok {
		return
	}
	ctx := r.Context()

	if h.deps.Storage == nil || h.deps.Files == nil {
		httperr.Write(r.Context(), w, http.StatusServiceUnavailable, "storage_unavailable", "no object storage backend is configured")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, h.deps.MaxLogoBytes+multipartOverheadBytes)
	file, header, err := r.FormFile("file")
	if err != nil {
		if _, tooLarge := errors.AsType[*http.MaxBytesError](err); tooLarge {
			httperr.Write(r.Context(), w, http.StatusRequestEntityTooLarge, "file_too_large", "logo exceeds the maximum allowed size")
			return
		}
		httperr.Write(r.Context(), w, http.StatusBadRequest, "invalid_request", `a multipart/form-data "file" field is required`)
		return
	}
	defer func() { _ = file.Close() }()
	if r.MultipartForm != nil {
		defer func() { _ = r.MultipartForm.RemoveAll() }()
	}
	if header.Size > h.deps.MaxLogoBytes {
		httperr.Write(r.Context(), w, http.StatusRequestEntityTooLarge, "file_too_large", "logo exceeds the maximum allowed size")
		return
	}

	// The type is sniffed from the bytes, not taken from the client, so a
	// file only named like an image is refused.
	head := make([]byte, 512)
	n, err := io.ReadFull(file, head)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		httperr.Write(r.Context(), w, http.StatusBadRequest, "invalid_request", "could not read uploaded file")
		return
	}
	head = head[:n]
	contentType := http.DetectContentType(head)
	ext, ok := logoTypes[contentType]
	if !ok {
		httperr.Write(r.Context(), w, http.StatusUnsupportedMediaType, "invalid_content_type", "a logo must be a PNG, JPEG, GIF or WebP image")
		return
	}

	fileID := uuid.NewV7().String()
	key := storage.BuildKey(logoPurpose, c.tenant.TenantID, fileID, ext)
	hasher := sha256.New()
	body := io.TeeReader(io.MultiReader(bytes.NewReader(head), file), hasher)
	if _, err := h.deps.Storage.Upload(ctx, key, body, storage.UploadOptions{ContentType: contentType, Public: true}); err != nil {
		httperr.Write(r.Context(), w, http.StatusServiceUnavailable, "storage_unavailable", "upload failed")
		return
	}
	logoURL, err := h.deps.Storage.PublicURL(ctx, key)
	if err != nil {
		_ = h.deps.Storage.Delete(ctx, key)
		writeInternalError(w, r, err, "resolve logo public url")
		return
	}

	if err := h.deps.Files.Insert(ctx, c.tenant.Slug, files.InsertRow{
		ID:             fileID,
		TenantID:       c.tenant.TenantID,
		StorageKey:     key,
		OriginalName:   header.Filename,
		ContentType:    contentType,
		SizeBytes:      header.Size,
		ChecksumSHA256: hex.EncodeToString(hasher.Sum(nil)),
		UploadedBy:     c.auth.UserID,
		Purpose:        logoPurpose,
		IsPublic:       true,
	}); err != nil {
		_ = h.deps.Storage.Delete(ctx, key)
		writeInternalError(w, r, err, "record logo file")
		return
	}

	previous, err := h.deps.TenantStore.SetLogoURL(ctx, c.tenant.TenantID, logoURL)
	if err != nil {
		h.retireLogoFile(ctx, c.tenant.Slug, fileID)
		writeInternalError(w, r, err, "set logo url")
		return
	}
	h.retireLogo(ctx, c.tenant.Slug, previous)
	h.recordAudit(r, c, "tenant.settings_updated", map[string]any{"fields": []string{"general.logo_url"}, "file_id": fileID})

	writeJSON(w, http.StatusOK, logoResponse{LogoURL: &logoURL})
}

// ServeDeleteLogo is DELETE /admin/settings/logo: it clears logo_url.
func (h *Handler) ServeDeleteLogo(w http.ResponseWriter, r *http.Request) {
	c, ok := h.authorize(w, r)
	if !ok {
		return
	}
	ctx := r.Context()

	previous, err := h.deps.TenantStore.SetLogoURL(ctx, c.tenant.TenantID, "")
	if err != nil {
		writeInternalError(w, r, err, "clear logo url")
		return
	}
	if previous != nil {
		h.retireLogo(ctx, c.tenant.Slug, previous)
		h.recordAudit(r, c, "tenant.settings_updated", map[string]any{"fields": []string{"general.logo_url"}})
	}
	writeJSON(w, http.StatusOK, logoResponse{})
}

// retireLogo deletes a logo that logo_url no longer points to, found by
// the file id its URL ends in (storage.BuildKey). The object is public,
// so it is deleted now rather than left for a sweep. Best-effort: the
// logo_url change has already committed.
func (h *Handler) retireLogo(ctx context.Context, tenantSlug string, logoURL *string) {
	if logoURL == nil {
		return
	}
	name := path.Base(*logoURL)
	id, err := uuid.Parse(strings.TrimSuffix(name, path.Ext(name)))
	if err != nil {
		log.Warn().Str("logo_url", *logoURL).Msg("adminsettings: replaced logo url names no file id, leaving its object")
		return
	}
	h.retireLogoFile(ctx, tenantSlug, id.String())
}

func (h *Handler) retireLogoFile(ctx context.Context, tenantSlug, fileID string) {
	if h.deps.Files == nil || h.deps.Storage == nil {
		return
	}
	f, err := h.deps.Files.GetByID(ctx, tenantSlug, fileID)
	if err != nil {
		log.Warn().Err(err).Str("file_id", fileID).Msg("adminsettings: look up retired logo")
		return
	}
	if f.Purpose != logoPurpose {
		return
	}
	if err := h.deps.Storage.Delete(ctx, f.StorageKey); err != nil {
		log.Warn().Err(err).Str("file_id", fileID).Msg("adminsettings: delete retired logo object")
	}
	if err := h.deps.Files.MarkDeleted(ctx, tenantSlug, fileID); err != nil {
		log.Warn().Err(err).Str("file_id", fileID).Msg("adminsettings: mark retired logo deleted")
	}
}
