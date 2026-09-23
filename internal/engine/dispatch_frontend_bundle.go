package engine

import (
	"io"
	"net/http"
	"regexp"
	"strconv"

	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/route"
	"github.com/rs/zerolog/log"
)

// bundleFilenamePattern is the only {file} GET /modules/{module}/frontend/{file}
// serves — module.BundleFilename's own output shape ("bundle." plus
// bundleFilenameHashLen lowercase hex characters plus ".js"). Anything else
// 404s without ever touching storage.
var bundleFilenamePattern = regexp.MustCompile(`^bundle\.[0-9a-f]{12}\.js$`)

// dispatchFrontendBundleRoute is GET /modules/{module}/frontend/{file}'s
// handler (goerp#588) — EngineBuiltin, same posture as /storage/upload:
// anonymous and tenant-independent, since a module's frontend bundle is
// module code, not tenant data, and the shell's own loader fetches it
// without credentials and verifies its digest itself
// (shell-architecture.md §10). Reads storage.Backend directly by
// module.BundleStorageKey(module, file) — never the live registry — so a
// previous version's bundle, published under its own filename's key, stays
// servable for a client still holding its URL even after a reload replaces
// the module's current version.
func (e *Engine) dispatchFrontendBundleRoute(w http.ResponseWriter, r *http.Request) {
	if e.storageBackend == nil {
		writeRouteError(w, http.StatusServiceUnavailable, "storage_unavailable", "no object storage backend is configured")
		return
	}

	params := route.ParamsFromContext(r.Context())
	moduleName, file := params["module"], params["file"]
	if !bundleFilenamePattern.MatchString(file) {
		writeRouteError(w, http.StatusNotFound, "not_found", "no such frontend bundle")
		return
	}

	ctx := r.Context()
	key := module.BundleStorageKey(moduleName, file)
	exists, err := e.storageBackend.Exists(ctx, key)
	if err != nil {
		writeRouteError(w, http.StatusInternalServerError, "internal_error", "check frontend bundle failed")
		return
	}
	if !exists {
		writeRouteError(w, http.StatusNotFound, "not_found", "no such frontend bundle")
		return
	}

	rc, size, err := e.storageBackend.Download(ctx, key)
	if err != nil {
		writeRouteError(w, http.StatusInternalServerError, "internal_error", "read frontend bundle failed")
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
