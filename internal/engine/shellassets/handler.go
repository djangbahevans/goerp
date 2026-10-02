// Package shellassets serves the shell build and its shared runtime import map.
package shellassets

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"io/fs"
	"net/http"
	"strings"
	"time"
)

const placeholder = "<!--goerp-import-map-->"

type importMap struct {
	Imports map[string]string `json:"imports"`
}

type Handler struct {
	html   []byte
	data   []byte
	assets http.Handler
	files  fs.FS
}

func New(build fs.FS) (*Handler, error) {
	html, err := fs.ReadFile(build, "index.html")
	if err != nil {
		return nil, fmt.Errorf("read shell HTML: %w", err)
	}
	if bytes.Count(html, []byte(placeholder)) != 1 {
		return nil, fmt.Errorf("shell HTML must contain exactly one import-map placeholder")
	}
	data, err := fs.ReadFile(build, "import-map.json")
	if err != nil {
		return nil, fmt.Errorf("read shell import map: %w", err)
	}
	var mapping importMap
	if err := json.Unmarshal(data, &mapping); err != nil {
		return nil, fmt.Errorf("parse shell import map: %w", err)
	}
	if len(mapping.Imports) == 0 {
		return nil, fmt.Errorf("shell import map must contain shared packages")
	}
	for name, url := range mapping.Imports {
		file, ok := strings.CutPrefix(url, "/assets/")
		if name == "" || !ok || !fs.ValidPath(file) || !strings.HasSuffix(file, ".js") {
			return nil, fmt.Errorf("invalid shell import %q: %q", name, url)
		}
		info, err := fs.Stat(build, strings.TrimPrefix(url, "/"))
		if err != nil {
			return nil, fmt.Errorf("check shell import %q: %w", name, err)
		}
		if info.IsDir() {
			return nil, fmt.Errorf("shell import %q is a directory", name)
		}
	}
	// Inline JSON must not be able to terminate its script element.
	data, err = json.Marshal(mapping, jsontext.EscapeForHTML(true))
	if err != nil {
		return nil, fmt.Errorf("encode shell import map: %w", err)
	}
	assets, err := fs.Sub(build, "assets")
	if err != nil {
		return nil, fmt.Errorf("open shell assets: %w", err)
	}
	return &Handler{html: html, data: data, assets: http.StripPrefix("/assets/", http.FileServerFS(assets)), files: assets}, nil
}

func (h *Handler) Wrap(next http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /__goerp_import_map", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		http.ServeContent(w, r, "import-map.json", time.Time{}, bytes.NewReader(h.data))
	})
	mux.HandleFunc("GET /assets/", func(w http.ResponseWriter, r *http.Request) {
		info, err := fs.Stat(h.files, strings.TrimPrefix(r.URL.Path, "/assets/"))
		if err != nil || info.IsDir() {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		h.assets.ServeHTTP(w, r)
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if path == "/__goerp_import_map" || strings.HasPrefix(path, "/assets/") {
			mux.ServeHTTP(w, r)
			return
		}
		builtIn := strings.HasPrefix(path, "/_") && !strings.HasPrefix(path, "/_m/")
		if (r.Method != http.MethodGet && r.Method != http.MethodHead) || builtIn || !strings.Contains(r.Header.Get("Accept"), "text/html") {
			next.ServeHTTP(w, r)
			return
		}
		body := bytes.Replace(h.html, []byte(placeholder), append(append([]byte(`<script type="importmap" id="goerp-import-map">`), h.data...), []byte("</script>")...), 1)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		http.ServeContent(w, r, "index.html", time.Time{}, bytes.NewReader(body))
	})
}
