package tenantimport

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/checkpoint"
	"github.com/djangbahevans/goerp/internal/engine/db"
	enginemanifest "github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/registry"
	"github.com/djangbahevans/goerp/internal/engine/schema"
	"github.com/djangbahevans/goerp/internal/engine/storage"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	tenantprovision "github.com/djangbahevans/goerp/internal/engine/tenant/provision"
	"github.com/djangbahevans/goerp/sdk/go/model"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
)

const localPostgresDSN = "postgres://goerp:dev@localhost:55432/goerp"

func openTestPrimaryDB(t *testing.T) *sql.DB {
	t.Helper()
	conn, err := db.New(localPostgresDSN)
	if err != nil {
		t.Skipf("postgres not reachable at %s (start compose.dev.yml): %v", localPostgresDSN, err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

func widgetModelDecl() model.ModelDeclaration {
	return model.ModelDeclaration{
		Name:  "widget",
		Table: "widgets",
		Fields: []model.NamedField{
			{Name: "id", Def: model.UUID().Required().PrimaryKey()},
			{Name: "name", Def: model.Text().Required()},
		},
	}
}

// importTestFixture bundles a real Worker wired against real Postgres and
// local storage, plus a module registry containing testmodule/widget —
// mirroring tenantexport's own worker_test.go fixture, so that package's
// output is exactly what this package's input needs to look like.
type importTestFixture struct {
	worker  *Worker
	conn    *sql.DB
	storage storage.Backend
	reg     *registry.ModuleRegistry
}

func newImportTestFixture(t *testing.T) *importTestFixture {
	t.Helper()
	ctx := context.Background()
	conn := openTestPrimaryDB(t)

	tenantStore := tenant.NewStore(conn)
	if err := tenantStore.Bootstrap(ctx); err != nil {
		t.Fatalf("tenant Bootstrap() error: %v", err)
	}
	checkpointStore := checkpoint.NewStore(conn)
	if err := checkpointStore.Bootstrap(ctx); err != nil {
		t.Fatalf("checkpoint Bootstrap() error: %v", err)
	}

	pool := schema.NewPool(conn, 5*time.Second)
	if err := pool.Bootstrap(ctx); err != nil {
		t.Fatalf("schema pool Bootstrap() error: %v", err)
	}
	diffEngine := schema.NewSchemaDiffEngine(&schema.Config{})

	reg := &registry.ModuleRegistry{}
	if _, err := reg.Update(map[string]*module.LoadedModule{
		"testmodule": {
			Status:     module.StatusReady,
			Manifest:   enginemanifest.Manifest{Name: "testmodule", Type: "standard", Version: "1.0.0"},
			ModelDecls: []model.ModelDeclaration{widgetModelDecl()},
		},
	}); err != nil {
		t.Fatalf("registry Update() error: %v", err)
	}

	provisionActivities := tenantprovision.NewActivities(tenantStore, nil, conn, pool, diffEngine, reg, "goerp.test")

	t.Setenv("GOERP_STORAGE_LOCAL_DIR", t.TempDir())
	backend, err := storage.New("local")
	if err != nil {
		t.Fatalf(`storage.New("local") error: %v`, err)
	}

	return &importTestFixture{
		worker: &Worker{
			TenantStore:    tenantStore,
			Registry:       reg,
			RawDB:          conn,
			Checkpoints:    checkpointStore,
			StorageBackend: backend,
			Provision:      provisionActivities,
		},
		conn:    conn,
		storage: backend,
		reg:     reg,
	}
}

func testJob(id int64, args Args) *river.Job[Args] {
	return &river.Job[Args]{JobRow: &rivertype.JobRow{ID: id, Attempt: 1, MaxAttempts: 3}, Args: args}
}

func testFinalAttemptJob(id int64, args Args) *river.Job[Args] {
	return &river.Job[Args]{JobRow: &rivertype.JobRow{ID: id, Attempt: 3, MaxAttempts: 3}, Args: args}
}

// encryptTestArchive builds a plaintext zip archive (manifest.json plus one
// .jsonl file per module) and AES-256-GCM encrypts it — mirroring
// tenantexport's own buildArchive/encryptArchive, duplicated here rather
// than imported (this package stays independent of tenantexport's
// unexported internals, same as production code never imports across that
// boundary either).
func encryptTestArchive(t *testing.T, man manifest, moduleData map[string][]byte) (ciphertext []byte, keyB64 string) {
	t.Helper()

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	manifestBytes, err := json.Marshal(man)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	mw, err := zw.Create("manifest.json")
	if err != nil {
		t.Fatalf("create manifest.json entry: %v", err)
	}
	if _, err := mw.Write(manifestBytes); err != nil {
		t.Fatalf("write manifest.json: %v", err)
	}
	for _, m := range man.Modules {
		fw, err := zw.Create(m.File)
		if err != nil {
			t.Fatalf("create %s entry: %v", m.File, err)
		}
		if _, err := fw.Write(moduleData[m.Name]); err != nil {
			t.Fatalf("write %s: %v", m.File, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip writer: %v", err)
	}

	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("generate key: %v", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatalf("aes.NewCipher: %v", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatalf("cipher.NewGCM: %v", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		t.Fatalf("generate nonce: %v", err)
	}
	sealed := gcm.Seal(nonce, nonce, buf.Bytes(), nil)
	return sealed, base64.RawURLEncoding.EncodeToString(key)
}

func jsonlLine(t *testing.T, rec exportRecord) []byte {
	t.Helper()
	data, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("marshal record: %v", err)
	}
	return append(data, '\n')
}

func TestWorkerRun_ImportsArchiveIntoNewTenant(t *testing.T) {
	f := newImportTestFixture(t)
	ctx := context.Background()

	widgetID := "11111111-1111-1111-1111-111111111111"
	moduleData := map[string][]byte{
		"testmodule": jsonlLine(t, exportRecord{Model: "widget", Record: map[string]any{"id": widgetID, "name": "Widget A"}}),
	}
	man := manifest{
		TenantSlug: "source-tenant",
		ExportedAt: time.Now().UTC(),
		Modules:    []manifestModule{{Name: "testmodule", Version: "1.0.0", File: "testmodule.jsonl"}},
	}
	ciphertext, keyB64 := encryptTestArchive(t, man, moduleData)

	inputRef := fmt.Sprintf("imports/%d/archive.zip.enc", time.Now().UnixNano())
	if _, err := f.storage.Upload(ctx, inputRef, bytes.NewReader(ciphertext), storage.UploadOptions{ContentType: "application/octet-stream"}); err != nil {
		t.Fatalf("upload test archive: %v", err)
	}

	slug := fmt.Sprintf("importtest%d", time.Now().UnixNano())
	jobID := time.Now().UnixNano()
	t.Cleanup(func() {
		_, _ = f.conn.Exec("DELETE FROM system.job_checkpoints WHERE job_id = $1", fmt.Sprintf("%d", jobID))
		_, _ = f.conn.Exec(`DROP SCHEMA IF EXISTS "tenant_` + slug + `" CASCADE`)
		_, _ = f.conn.Exec("DELETE FROM system.tenants WHERE slug = $1", slug)
	})

	result, err := f.worker.run(ctx, testJob(jobID, Args{NewSlug: slug, InputRef: inputRef, DecryptionKey: keyB64}))
	if err != nil {
		t.Fatalf("run() error: %v", err)
	}
	if result.TenantSlug != slug {
		t.Errorf("TenantSlug = %q, want %q", result.TenantSlug, slug)
	}
	if len(result.ModulesImported) != 1 || result.ModulesImported[0] != "testmodule" {
		t.Errorf("ModulesImported = %v, want [testmodule]", result.ModulesImported)
	}

	tt, err := f.worker.TenantStore.GetBySlug(ctx, slug)
	if err != nil {
		t.Fatalf("GetBySlug() error: %v", err)
	}
	if tt.Status != tenant.StatusActive {
		t.Errorf("tenant status = %q, want active", tt.Status)
	}

	var name string
	err = f.conn.QueryRow(`SELECT name FROM "tenant_`+slug+`".widgets WHERE id = $1`, widgetID).Scan(&name)
	if err != nil {
		t.Fatalf("query imported row: %v", err)
	}
	if name != "Widget A" {
		t.Errorf("imported widget name = %q, want %q", name, "Widget A")
	}
}

func TestWorkerRun_RejectsModuleVersionMismatch(t *testing.T) {
	f := newImportTestFixture(t)
	ctx := context.Background()

	moduleData := map[string][]byte{
		"testmodule": jsonlLine(t, exportRecord{Model: "widget", Record: map[string]any{"id": "11111111-1111-1111-1111-111111111111", "name": "Widget A"}}),
	}
	man := manifest{
		TenantSlug: "source-tenant",
		ExportedAt: time.Now().UTC(),
		Modules:    []manifestModule{{Name: "testmodule", Version: "9.9.9", File: "testmodule.jsonl"}},
	}
	ciphertext, keyB64 := encryptTestArchive(t, man, moduleData)

	inputRef := fmt.Sprintf("imports/%d/archive.zip.enc", time.Now().UnixNano())
	if _, err := f.storage.Upload(ctx, inputRef, bytes.NewReader(ciphertext), storage.UploadOptions{ContentType: "application/octet-stream"}); err != nil {
		t.Fatalf("upload test archive: %v", err)
	}

	slug := fmt.Sprintf("importtest%d", time.Now().UnixNano())
	jobID := time.Now().UnixNano()
	t.Cleanup(func() {
		_, _ = f.conn.Exec("DELETE FROM system.job_checkpoints WHERE job_id = $1", fmt.Sprintf("%d", jobID))
		_, _ = f.conn.Exec("DELETE FROM system.tenants WHERE slug = $1", slug)
	})

	_, err := f.worker.run(ctx, testJob(jobID, Args{NewSlug: slug, InputRef: inputRef, DecryptionKey: keyB64}))
	if err == nil {
		t.Fatal("run() error = nil, want a version-mismatch error")
	}

	if _, getErr := f.worker.TenantStore.GetBySlug(ctx, slug); getErr == nil {
		t.Error("tenant was created despite the version mismatch — version check must run before tenant creation")
	}
}

// TestWorkerRun_FinalAttemptFailureReleasesSlugReservation exercises run's
// own compensation path: a module-load failure (an archive record naming a
// model the module never declared) on what testFinalAttemptJob marks as
// River's last attempt must release the tenant it already created via
// TenantStore.DeleteProvisioning, not leave it stuck at StatusProvisioning
// forever.
func TestWorkerRun_FinalAttemptFailureReleasesSlugReservation(t *testing.T) {
	f := newImportTestFixture(t)
	ctx := context.Background()

	moduleData := map[string][]byte{
		"testmodule": jsonlLine(t, exportRecord{Model: "no-such-model", Record: map[string]any{"id": "11111111-1111-1111-1111-111111111111", "name": "Widget A"}}),
	}
	man := manifest{
		TenantSlug: "source-tenant",
		ExportedAt: time.Now().UTC(),
		Modules:    []manifestModule{{Name: "testmodule", Version: "1.0.0", File: "testmodule.jsonl"}},
	}
	ciphertext, keyB64 := encryptTestArchive(t, man, moduleData)

	inputRef := fmt.Sprintf("imports/%d/archive.zip.enc", time.Now().UnixNano())
	if _, err := f.storage.Upload(ctx, inputRef, bytes.NewReader(ciphertext), storage.UploadOptions{ContentType: "application/octet-stream"}); err != nil {
		t.Fatalf("upload test archive: %v", err)
	}

	slug := fmt.Sprintf("importtest%d", time.Now().UnixNano())
	jobID := time.Now().UnixNano()
	t.Cleanup(func() {
		_, _ = f.conn.Exec("DELETE FROM system.job_checkpoints WHERE job_id = $1", fmt.Sprintf("%d", jobID))
		_, _ = f.conn.Exec(`DROP SCHEMA IF EXISTS "tenant_` + slug + `" CASCADE`)
		_, _ = f.conn.Exec("DELETE FROM system.tenants WHERE slug = $1", slug)
	})

	_, err := f.worker.run(ctx, testFinalAttemptJob(jobID, Args{NewSlug: slug, InputRef: inputRef, DecryptionKey: keyB64}))
	if err == nil {
		t.Fatal("run() error = nil, want a load-module error")
	}

	if _, getErr := f.worker.TenantStore.GetBySlug(ctx, slug); !errors.Is(getErr, tenant.ErrTenantNotFound) {
		t.Errorf("GetBySlug() error = %v, want ErrTenantNotFound — slug reservation should have been released", getErr)
	}
}
