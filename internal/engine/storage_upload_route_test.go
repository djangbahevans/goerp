package engine

import (
	"bytes"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/djangbahevans/goerp/internal/engine/files"
	"github.com/djangbahevans/goerp/internal/engine/storage"
	"github.com/djangbahevans/goerp/internal/engine/storageupload"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
)

// TestBuildChain_StorageUploadReachesHandlerThroughRealRouteTable guards
// goerp#818's own review finding: adding a handler to the builtins map
// engine.go passes into buildChain isn't enough on its own — the request
// never reaches it unless the path is also registered in
// registry.registerBuiltinRoutes, since routeResolutionMiddleware 404s on
// any path RouteTable.Lookup doesn't know about, before builtins is ever
// consulted. storageupload's own package tests call Handler.ServeHTTP
// directly and can't catch this gap; only a real buildChain request can.
func TestBuildChain_StorageUploadReachesHandlerThroughRealRouteTable(t *testing.T) {
	f := newChainFixture(t)

	t.Setenv("GOERP_STORAGE_LOCAL_DIR", t.TempDir())
	backend, err := storage.New("local")
	if err != nil {
		t.Fatalf("storage.New() error: %v", err)
	}
	filesStore := files.NewStore(f.conn)
	if err := filesStore.Bootstrap(t.Context(), f.tenantSlug); err != nil {
		t.Fatalf("files Bootstrap() error: %v", err)
	}

	uploadHandler := storageupload.NewHandler(f.resolver, f.checker, backend, filesStore, storageupload.Limits{MaxFileBytes: 1 << 20})
	h := f.chain(map[string]http.Handler{"POST /storage/upload": uploadHandler})

	body := &bytes.Buffer{}
	w := multipart.NewWriter(body)
	part, err := w.CreateFormFile("file", "hello.txt")
	if err != nil {
		t.Fatalf("CreateFormFile() error: %v", err)
	}
	if _, err := part.Write([]byte("hello world")); err != nil {
		t.Fatalf("write file part: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/storage/upload", body)
	req.Host = f.domain
	req.Header.Set("Content-Type", "multipart/form-data; boundary="+w.Boundary())
	req.Header.Set("Authorization", "Bearer "+f.issueToken(t))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		FileID string `json:"file_id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp.FileID == "" {
		t.Error("file_id is empty")
	}

	var count int
	if err := f.conn.QueryRow(fmt.Sprintf("SELECT count(*) FROM %s.files WHERE id = $1", tenantschema.Name(f.tenantSlug)), resp.FileID).Scan(&count); err != nil {
		t.Fatalf("count files rows: %v", err)
	}
	if count != 1 {
		t.Errorf("files row count for %q = %d, want 1", resp.FileID, count)
	}
}
