package engine

import (
	"context"
	"encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
	"uuid"

	"github.com/djangbahevans/goerp/internal/engine/config"
	"github.com/djangbahevans/goerp/internal/engine/files"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/storage"
	"github.com/djangbahevans/goerp/internal/engine/wasm"
	"github.com/djangbahevans/goerp/internal/engine/wasm/wasmtest"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

type dispatchFileBackend struct{ storage.Backend }

func (dispatchFileBackend) SignedURL(context.Context, string, time.Duration) (string, error) {
	return "https://files.example.test/signature.png", nil
}

func TestDispatchORMFile(t *testing.T) {
	f := newDispatchORMFixture(t)
	conn := f.e.primaryDB
	store := files.NewStore(conn)
	if err := store.Bootstrap(t.Context(), f.slug); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(t.Context(), "ALTER TABLE tenant_"+f.slug+".widget ADD COLUMN signed_pdf_id UUID REFERENCES tenant_"+f.slug+".files(id)"); err != nil {
		t.Fatal(err)
	}

	md := widgetModelDecl()
	md.Fields = append(md.Fields, model.NamedField{Name: "signed_pdf_id", Def: model.File()})
	mod := *f.e.moduleRegistry.Snapshot().Modules()["testmodule"]
	mod.ModelDecls = []model.ModelDeclaration{md}
	if _, err := f.e.moduleRegistry.Update(map[string]*module.LoadedModule{"testmodule": &mod}); err != nil {
		t.Fatal(err)
	}

	rt, err := wasm.New(&config.Config{
		CompilationCache:            wasmtest.SharedCompilationCacheDir(),
		Environment:                 string(config.Production),
		PoolMaxMemoryByes:           1 << 20,
		DBMaxConcurrentTransactions: 10,
	}, conn, dispatchFileBackend{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rt.Close(context.Background()) })
	f.e.wasmRuntime = rt

	fileID := uuid.NewV7().String()
	if err := store.Insert(t.Context(), f.slug, files.InsertRow{
		ID:             fileID,
		TenantID:       f.tenantID,
		StorageKey:     "attachments/signature.png",
		OriginalName:   "signature.png",
		ContentType:    "image/png",
		SizeBytes:      128,
		ChecksumSHA256: "checksum",
		Purpose:        "attachments",
	}); err != nil {
		t.Fatal(err)
	}

	body, err := json.Marshal(map[string]any{"name": "Signed contract", "signed_pdf_id": fileID})
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	f.e.dispatchORMRoute(w, f.request(http.MethodPost, "/testmodule/widgets", body, f.entryCreate, nil))
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}

	var created map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	id := created["id"].(string)
	for _, entry := range []struct {
		method, path string
		list         bool
	}{
		{method: http.MethodGet, path: "/testmodule/widgets/" + id},
		{method: http.MethodGet, path: "/testmodule/widgets", list: true},
	} {
		w = httptest.NewRecorder()
		route := f.entryGet
		if entry.list {
			route = f.entryList
		}
		f.e.dispatchORMRoute(w, f.request(entry.method, entry.path, nil, route, map[string]string{"id": id}))
		if w.Code != http.StatusOK {
			t.Fatalf("read: %d %s", w.Code, w.Body.String())
		}

		if !entry.list {
			var row map[string]any
			if err := json.Unmarshal(w.Body.Bytes(), &row); err != nil {
				t.Fatal(err)
			}
			ref := row["signed_pdf"].(map[string]any)
			if row["signed_pdf_id"] != fileID || ref["name"] != "signature.png" || ref["url"] != "https://files.example.test/signature.png" || ref["url_expires_at"] == nil {
				t.Fatalf("HTTP file: %s", w.Body.String())
			}
			t.Logf("file response: %s", w.Body.String())
		}
	}

	invalidBody := []byte(`{"name":"Invalid", "signed_pdf_id":"malformed"}`)
	w = httptest.NewRecorder()
	f.e.dispatchORMRoute(w, f.request(http.MethodPost, "/testmodule/widgets", invalidBody, f.entryCreate, nil))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("invalid reference: %d %s", w.Code, w.Body.String())
	}
}
