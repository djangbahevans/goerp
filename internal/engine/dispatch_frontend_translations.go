package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/djangbahevans/goerp/internal/engine/l10n"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/route"
	"github.com/rs/zerolog/log"
)

// maxFrontendTranslationSize bounds how much of a stored translation file
// the route reads into memory to hash for its ETag.
const maxFrontendTranslationSize = 8 << 20

// dispatchFrontendTranslationsRoute is GET
// /modules/{module}/translations/{locale}.json (l10n-guide.md §7
// "Translation loading", goerp#1121): the loaded module's
// frontend/translations/{locale}.json from the live translation set of the
// version currently loaded (module.LiveFrontendTranslationKey). Anonymous and tenant-independent
// like the bundle route. The URL carries no version, so unlike the bundle
// it can't be cached as immutable: it sends an ETag of the content and
// no-cache, so a client revalidates cheaply and sees a new version's
// strings as soon as the module reloads.
func (e *Engine) dispatchFrontendTranslationsRoute(w http.ResponseWriter, r *http.Request) {
	params := route.ParamsFromContext(r.Context())
	moduleName := params["module"]
	locale, ok := strings.CutSuffix(params["file"], ".json")
	if !ok {
		writeRouteError(w, http.StatusNotFound, "not_found", "no such translation file")
		return
	}
	if !l10n.ValidLocale(locale) {
		writeRouteError(w, http.StatusBadRequest, "invalid_locale", "locale must be a BCP 47 tag such as en or pt-BR")
		return
	}

	snap := e.moduleRegistry.Snapshot()
	if snap == nil {
		writeRouteError(w, http.StatusServiceUnavailable, "not_ready", "engine has not finished starting")
		return
	}
	m, ok := snap.Modules()[moduleName]
	if !ok || m.Status != module.StatusReady {
		writeRouteError(w, http.StatusNotFound, "not_found", "no such module")
		return
	}
	if e.storageBackend == nil {
		writeRouteError(w, http.StatusServiceUnavailable, "storage_unavailable", "no object storage backend is configured")
		return
	}

	ctx := r.Context()
	key, err := module.LiveFrontendTranslationKey(ctx, e.storageBackend, moduleName, m.Manifest.Version, locale)
	if err != nil {
		writeRouteError(w, http.StatusInternalServerError, "internal_error", "check translation file failed")
		return
	}
	exists := false
	if key != "" {
		if exists, err = e.storageBackend.Exists(ctx, key); err != nil {
			writeRouteError(w, http.StatusInternalServerError, "internal_error", "check translation file failed")
			return
		}
	}
	if !exists {
		writeRouteError(w, http.StatusNotFound, "not_found", "no such translation file")
		return
	}

	rc, _, err := e.storageBackend.Download(ctx, key)
	if err != nil {
		writeRouteError(w, http.StatusInternalServerError, "internal_error", "read translation file failed")
		return
	}
	defer func() { _ = rc.Close() }()
	data, err := io.ReadAll(io.LimitReader(rc, maxFrontendTranslationSize+1))
	if err != nil || len(data) > maxFrontendTranslationSize {
		writeRouteError(w, http.StatusInternalServerError, "internal_error", "read translation file failed")
		return
	}

	sum := sha256.Sum256(data)
	etag := `"` + hex.EncodeToString(sum[:16]) + `"`
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "public, no-cache")
	if etagMatches(r.Header.Get("If-None-Match"), etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(data); err != nil {
		log.Warn().Err(err).Str("module", moduleName).Str("locale", locale).Msg("serve frontend translations: write response failed")
	}
}

// etagMatches reports whether an If-None-Match header value (a list of
// ETags, possibly weak, or "*") names etag.
func etagMatches(header, etag string) bool {
	for candidate := range strings.SplitSeq(header, ",") {
		candidate = strings.TrimPrefix(strings.TrimSpace(candidate), "W/")
		if candidate == "*" || candidate == etag {
			return true
		}
	}
	return false
}
