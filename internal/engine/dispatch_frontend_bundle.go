package engine

import (
	"io"
	"net/http"
	"regexp"
	"strconv"

	"github.com/djangbahevans/goerp/internal/engine/httperr"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/route"
	"github.com/rs/zerolog/log"
)

// bundleFilenamePattern is the only {file} GET /modules/{module}/frontend/{file}
// serves — module.BundleFilename's own output shape ("bundle." plus
// bundleFilenameHashLen lowercase hex characters plus ".js"). Anything else
// 404s without ever touching storage.
var bundleFilenamePattern = regexp.MustCompile(`^bundle\.[0-9a-f]{12}\.js$`)

// Frontend bundles are anonymous module code. Content-specific storage keys keep older
// bundle URLs servable after a reload.
func (e *Engine) dispatchFrontendBundleRoute(w http.ResponseWriter, r *http.Request) {
	if e.storageBackend == nil {
		httperr.Write(r.Context(), w, http.StatusServiceUnavailable, "storage_unavailable", "no object storage backend is configured")
		return
	}

	params := route.ParamsFromContext(r.Context())
	moduleName, file := params["module"], params["file"]
	if !bundleFilenamePattern.MatchString(file) {
		httperr.Write(r.Context(), w, http.StatusNotFound, "not_found", "no such frontend bundle")
		return
	}

	ctx := r.Context()
	key := module.BundleStorageKey(moduleName, file)
	exists, err := e.storageBackend.Exists(ctx, key)
	if err != nil {
		httperr.Write(r.Context(), w, http.StatusInternalServerError, "internal_error", "check frontend bundle failed")
		return
	}
	if !exists {
		httperr.Write(r.Context(), w, http.StatusNotFound, "not_found", "no such frontend bundle")
		return
	}

	rc, size, err := e.storageBackend.Download(ctx, key)
	if err != nil {
		httperr.Write(r.Context(), w, http.StatusInternalServerError, "internal_error", "read frontend bundle failed")
		return
	}
	defer func() { _ = rc.Close() }()

	w.Header().Set("Content-Type", "text/javascript")
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	w.WriteHeader(http.StatusOK)
	if _, err := io.Copy(w, rc); err != nil {
		log.Warn().Err(err).Str("module", moduleName).Str("file", file).Msg("serve frontend bundle: write response failed")
	}
}
