package shellassets

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func fixture() fstest.MapFS {
	return fstest.MapFS{
		"index.html":           {Data: []byte(`<head><!--goerp-import-map--><script type="module" src="/assets/app-hash.js"></script></head>`)},
		"import-map.json":      {Data: []byte(`{"imports":{"react":"/assets/react-hash.js"}}`)},
		"assets/react-hash.js": {Data: []byte(`export const version = "19";`)},
		"assets/app-hash.js":   {Data: []byte(`import "./react-hash.js";`)},
		"private.txt":          {Data: []byte("not public")},
	}
}

func TestHTMLAndSharedEntries(t *testing.T) {
	h, err := New(fixture())
	if err != nil {
		t.Fatal(err)
	}
	handler := h.Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) }))
	for _, path := range []string{"/", "/auth/login", "/_m/demo/dashboard"} {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		r.Header.Set("Accept", "text/html,application/xhtml+xml")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		body := w.Body.String()
		if w.Code != http.StatusOK || !strings.Contains(body, `"react":"/assets/react-hash.js"`) {
			t.Fatalf("%s: %d %s", path, w.Code, body)
		}
		if strings.Index(body, `type="importmap"`) > strings.Index(body, `type="module"`) {
			t.Fatal("import map follows module script")
		}
		if w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("HTML can be cached")
		}
	}
	for _, path := range []string{"/assets/react-hash.js", "/__goerp_import_map"} {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
		}
		if path == "/assets/react-hash.js" && w.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" {
			t.Fatal("asset is not immutable")
		}
		if path == "/__goerp_import_map" && w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("import map can be cached")
		}
	}
}

func TestDelegatesAPIRequests(t *testing.T) {
	h, err := New(fixture())
	if err != nil {
		t.Fatal(err)
	}
	handler := h.Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) }))
	for _, tt := range []struct{ method, path, accept string }{
		{"GET", "/demo/items", "application/json"},
		{"GET", "/_health", "text/html"},
		{"GET", "/_reports/download/token", "text/html"},
		{"GET", "/_meta/schema", "text/html"},
		{"POST", "/auth/login", "text/html"},
		{"GET", "/private.txt", "*/*"},
	} {
		r := httptest.NewRequest(tt.method, tt.path, nil)
		r.Header.Set("Accept", tt.accept)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != http.StatusTeapot {
			t.Fatalf("%+v: %d", tt, w.Code)
		}
	}
	for _, path := range []string{"/assets/missing.js", "/assets/../private.txt"} {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if strings.Contains(w.Body.String(), "not public") || w.Code == http.StatusOK {
			t.Fatalf("exposed %s: %d %s", path, w.Code, w.Body.String())
		}
	}
}

func TestHeadAndUnsupportedMethods(t *testing.T) {
	h, err := New(fixture())
	if err != nil {
		t.Fatal(err)
	}
	handler := h.Wrap(http.NotFoundHandler())
	for _, path := range []string{"/", "/assets/react-hash.js", "/__goerp_import_map"} {
		r := httptest.NewRequest(http.MethodHead, path, nil)
		r.Header.Set("Accept", "text/html")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != http.StatusOK || w.Body.Len() != 0 {
			t.Fatalf("HEAD %s: %d %s", path, w.Code, w.Body.String())
		}
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/__goerp_import_map", nil))
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST import map: %d", w.Code)
	}
}

func TestRejectsIncompleteBuild(t *testing.T) {
	for _, tt := range []struct{ name, file, data string }{
		{"missing placeholder", "index.html", "<head></head>"},
		{"duplicate placeholder", "index.html", placeholder + placeholder},
		{"empty map", "import-map.json", `{"imports":{}}`},
		{"invalid JSON", "import-map.json", "{"},
		{"missing entry", "import-map.json", `{"imports":{"react":"/assets/missing.js"}}`},
		{"external URL", "import-map.json", `{"imports":{"react":"https://cdn.test/react.js"}}`},
		{"traversal", "import-map.json", `{"imports":{"react":"/assets/../private.js"}}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			build := fixture()
			build[tt.file] = &fstest.MapFile{Data: []byte(tt.data)}
			if _, err := New(build); err == nil {
				t.Fatal("accepted invalid shell build")
			}
		})
	}
}

func TestInlineJSONEscapesScriptTermination(t *testing.T) {
	build := fixture()
	build["import-map.json"].Data = []byte(`{"imports":{"</script><script>alert(1)</script>":"/assets/react-hash.js"}}`)
	h, err := New(build)
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("Accept", "text/html")
	h.Wrap(http.NotFoundHandler()).ServeHTTP(w, r)
	if strings.Contains(w.Body.String(), "<script>alert") {
		t.Fatal("unescaped inline JSON")
	}
}
