package engine

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/registry"
	"github.com/djangbahevans/goerp/internal/engine/route"
)

func translationsRequest(moduleName, file string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/modules/"+moduleName+"/translations/"+file, nil)
	ctx := route.WithParams(r.Context(), map[string]string{"module": moduleName, "file": file})
	return r.WithContext(ctx)
}

// translationsEngine has contacts@1.2.0 loaded and ready, with en.json and
// fr.json published for that version, and drafts loaded but failed.
func translationsEngine(t *testing.T) (*Engine, *fakeBundleBackend) {
	t.Helper()
	backend := &fakeBundleBackend{}
	files := map[string][]byte{"en": []byte(`{"actions.create":"New Contact"}`), "fr": []byte(`{"actions.create":"Nouveau contact"}`)}
	if err := module.PublishFrontendTranslations(t.Context(), backend, "contacts", "1.2.0", files); err != nil {
		t.Fatalf("PublishFrontendTranslations() error: %v", err)
	}
	reg := &registry.ModuleRegistry{}
	if _, err := reg.Update(map[string]*module.LoadedModule{
		"contacts": {Status: module.StatusReady, Manifest: manifest.Manifest{Name: "contacts", Version: "1.2.0", Type: "standard"}},
		"drafts":   {Status: module.StatusFailed, Manifest: manifest.Manifest{Name: "drafts", Version: "0.1.0", Type: "standard"}},
	}); err != nil {
		t.Fatalf("registry Update() error: %v", err)
	}
	return &Engine{storageBackend: backend, moduleRegistry: reg}, backend
}

func TestDispatchFrontendTranslationsRoute_ServesEachLocale(t *testing.T) {
	e, _ := translationsEngine(t)
	for file, want := range map[string]string{
		"en.json": `{"actions.create":"New Contact"}`,
		"fr.json": `{"actions.create":"Nouveau contact"}`,
	} {
		w := httptest.NewRecorder()
		e.dispatchFrontendTranslationsRoute(w, translationsRequest("contacts", file))
		if w.Code != http.StatusOK {
			t.Fatalf("%s status = %d, want 200; body: %s", file, w.Code, w.Body.String())
		}
		if got := w.Body.String(); got != want {
			t.Errorf("%s body = %s, want %s", file, got, want)
		}
		if got := w.Header().Get("Content-Type"); got != "application/json; charset=utf-8" {
			t.Errorf("%s Content-Type = %q", file, got)
		}
		if got := w.Header().Get("Cache-Control"); got != "public, no-cache" {
			t.Errorf("%s Cache-Control = %q, want %q", file, got, "public, no-cache")
		}
		if w.Header().Get("ETag") == "" {
			t.Errorf("%s has no ETag", file)
		}
	}
}

func TestDispatchFrontendTranslationsRoute_RevalidatesByETag(t *testing.T) {
	e, _ := translationsEngine(t)
	first := httptest.NewRecorder()
	e.dispatchFrontendTranslationsRoute(first, translationsRequest("contacts", "en.json"))
	etag := first.Header().Get("ETag")

	for _, header := range []string{etag, `"other", ` + etag, "W/" + etag, "*"} {
		r := translationsRequest("contacts", "en.json")
		r.Header.Set("If-None-Match", header)
		w := httptest.NewRecorder()
		e.dispatchFrontendTranslationsRoute(w, r)
		if w.Code != http.StatusNotModified || w.Body.Len() != 0 {
			t.Errorf("If-None-Match %s: status = %d, body %q, want an empty 304", header, w.Code, w.Body.String())
		}
	}

	r := translationsRequest("contacts", "fr.json")
	r.Header.Set("If-None-Match", etag)
	w := httptest.NewRecorder()
	e.dispatchFrontendTranslationsRoute(w, r)
	if w.Code != http.StatusOK {
		t.Errorf("fr.json with en.json's ETag: status = %d, want 200", w.Code)
	}
}

func TestDispatchFrontendTranslationsRoute_NotFound(t *testing.T) {
	e, _ := translationsEngine(t)
	for _, tc := range []struct{ module, file string }{
		{"missing", "en.json"}, // not installed
		{"drafts", "en.json"},  // loaded but failed
		{"contacts", "ar.json"},
		{"contacts", "en.js"},
		{"contacts", "en"},
	} {
		w := httptest.NewRecorder()
		e.dispatchFrontendTranslationsRoute(w, translationsRequest(tc.module, tc.file))
		if w.Code != http.StatusNotFound {
			t.Errorf("GET /modules/%s/translations/%s = %d, want 404", tc.module, tc.file, w.Code)
		}
	}
}

func TestDispatchFrontendTranslationsRoute_ServesOnlyTheLoadedVersionsLiveSet(t *testing.T) {
	e, backend := translationsEngine(t)
	if err := module.PublishFrontendTranslations(t.Context(), backend, "contacts", "1.1.0", map[string][]byte{"ar": []byte(`{}`)}); err != nil {
		t.Fatalf("publish 1.1.0: %v", err)
	}
	if _, err := module.UploadFrontendTranslations(t.Context(), backend, "contacts", "1.2.0", map[string][]byte{"de": []byte(`{}`)}); err != nil {
		t.Fatalf("upload an inactive 1.2.0 set: %v", err)
	}
	for _, file := range []string{"ar.json", "de.json"} {
		w := httptest.NewRecorder()
		e.dispatchFrontendTranslationsRoute(w, translationsRequest("contacts", file))
		if w.Code != http.StatusNotFound {
			t.Errorf("%s (another version's, or an inactive set's) = %d, want 404", file, w.Code)
		}
	}
}

func TestDispatchFrontendTranslationsRoute_VersionWithoutALiveSet404s(t *testing.T) {
	e, backend := translationsEngine(t)
	if err := module.ActivateFrontendTranslations(t.Context(), backend, "contacts", "1.2.0", ""); err != nil {
		t.Fatalf("clear the live set: %v", err)
	}
	w := httptest.NewRecorder()
	e.dispatchFrontendTranslationsRoute(w, translationsRequest("contacts", "en.json"))
	if w.Code != http.StatusNotFound {
		t.Errorf("en.json after clearing the live set = %d, want 404", w.Code)
	}
}

func TestDispatchFrontendTranslationsRoute_MalformedLocaleIsRejectedBeforeStorage(t *testing.T) {
	e := &Engine{} // no registry or storage: a malformed locale must never reach either
	for _, file := range []string{"..json", "../en.json", "EN.json", "en_US.json", "en-us.json", "english.json", ".json"} {
		w := httptest.NewRecorder()
		e.dispatchFrontendTranslationsRoute(w, translationsRequest("contacts", file))
		if w.Code != http.StatusBadRequest {
			t.Errorf("GET /modules/contacts/translations/%s = %d, want 400", file, w.Code)
		}
	}
}

func TestDispatchFrontendTranslationsRoute_NoStorageBackendReturns503(t *testing.T) {
	e, _ := translationsEngine(t)
	e.storageBackend = nil
	w := httptest.NewRecorder()
	e.dispatchFrontendTranslationsRoute(w, translationsRequest("contacts", "en.json"))
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", w.Code)
	}
}
