package tenantexport

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/checkpoint"
	"github.com/djangbahevans/goerp/internal/engine/db"
	enginemanifest "github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/registry"
	"github.com/djangbahevans/goerp/internal/engine/storage"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
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
			{Name: "credit_limit", Def: model.Integer().Access(model.AccessRead("contacts:contact:financials_read"))},
		},
	}
}

// exportTestFixture bundles what TestWorkerRun_* needs: a real Worker
// wired against real Postgres/local storage, plus the tenant/schema
// details needed to build a job's Args and inspect its result.
type exportTestFixture struct {
	worker     *Worker
	tenantID   string
	tenantSlug string
}

func newExportTestFixture(t *testing.T) *exportTestFixture {
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

	slug := fmt.Sprintf("exporttest%d", time.Now().UnixNano())
	tt, err := tenantStore.CreateTenant(ctx, slug, "Export Test Co")
	if err != nil {
		t.Fatalf("CreateTenant() error: %v", err)
	}
	t.Cleanup(func() { _, _ = conn.Exec("DELETE FROM system.tenants WHERE id = $1", tt.ID) })

	schema := tenantschema.Name(slug)
	if _, err := conn.Exec("CREATE SCHEMA " + schema); err != nil {
		t.Fatalf("create fixture schema: %v", err)
	}
	t.Cleanup(func() { _, _ = conn.Exec("DROP SCHEMA " + schema + " CASCADE") })

	if _, err := conn.Exec(fmt.Sprintf(`CREATE TABLE %s.widgets (
		id UUID PRIMARY KEY,
		name TEXT NOT NULL,
		credit_limit INTEGER
	)`, schema)); err != nil {
		t.Fatalf("create widgets table: %v", err)
	}
	if _, err := conn.Exec(fmt.Sprintf(
		"INSERT INTO %s.widgets (id, name, credit_limit) VALUES ($1, $2, $3)", schema,
	), "11111111-1111-1111-1111-111111111111", "Widget A", 5000); err != nil {
		t.Fatalf("insert fixture row: %v", err)
	}

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

	t.Setenv("GOERP_STORAGE_LOCAL_DIR", t.TempDir())
	backend, err := storage.New("local")
	if err != nil {
		t.Fatalf(`storage.New("local") error: %v`, err)
	}

	return &exportTestFixture{
		worker: &Worker{
			TenantStore:    tenantStore,
			Registry:       reg,
			RawDB:          conn,
			Checkpoints:    checkpointStore,
			StorageBackend: backend,
		},
		tenantID:   tt.ID,
		tenantSlug: tt.Slug,
	}
}

func testJob(id int64, args Args) *river.Job[Args] {
	return &river.Job[Args]{JobRow: &rivertype.JobRow{ID: id, Attempt: 1}, Args: args}
}

// decryptArchive mirrors what a real goerp tenant export client does with
// the returned decryption key — used here to verify the archive Worker.run
// actually produced is genuinely decryptable and contains what's expected.
func decryptArchive(t *testing.T, ciphertext []byte, keyB64 string) []byte {
	t.Helper()
	key, err := base64.RawURLEncoding.DecodeString(keyB64)
	if err != nil {
		t.Fatalf("decode key: %v", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatalf("aes.NewCipher: %v", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatalf("cipher.NewGCM: %v", err)
	}
	nonceSize := gcm.NonceSize()
	nonce, sealed := ciphertext[:nonceSize], ciphertext[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, sealed, nil)
	if err != nil {
		t.Fatalf("gcm.Open: %v", err)
	}
	return plaintext
}

func TestWorkerRun_ProducesDecryptableArchiveExcludingRestrictedField(t *testing.T) {
	f := newExportTestFixture(t)
	ctx := context.Background()
	jobID := time.Now().UnixNano()
	t.Cleanup(func() {
		_, _ = f.worker.RawDB.Exec("DELETE FROM system.job_checkpoints WHERE job_id = $1", fmt.Sprintf("%d", jobID))
	})

	result, err := f.worker.run(ctx, testJob(jobID, Args{TenantID: f.tenantID, TenantSlug: f.tenantSlug}))
	if err != nil {
		t.Fatalf("run() error: %v", err)
	}
	if result.DownloadURL == "" || result.Checksum == "" || result.DecryptionKey == "" {
		t.Fatalf("incomplete result: %+v", result)
	}

	archiveKey := fmt.Sprintf("exports/%s/%d/archive.zip.enc", f.tenantID, jobID)
	rc, _, err := f.worker.StorageBackend.Download(ctx, archiveKey)
	if err != nil {
		t.Fatalf("download archive: %v", err)
	}
	defer rc.Close()
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(rc); err != nil {
		t.Fatalf("read archive: %v", err)
	}
	ciphertext := buf.Bytes()

	if got := checksumHex(ciphertext); got != result.Checksum {
		t.Errorf("checksum mismatch: got %q, want %q", got, result.Checksum)
	}

	plaintext := decryptArchive(t, ciphertext, result.DecryptionKey)

	zr, err := zip.NewReader(bytes.NewReader(plaintext), int64(len(plaintext)))
	if err != nil {
		t.Fatalf("open decrypted archive as zip: %v", err)
	}
	zf, err := zr.Open("testmodule.jsonl")
	if err != nil {
		t.Fatalf("open testmodule.jsonl: %v", err)
	}
	defer zf.Close()

	var rec exportRecord
	if err := json.NewDecoder(zf).Decode(&rec); err != nil {
		t.Fatalf("decode exported record: %v", err)
	}
	if rec.Record["name"] != "Widget A" {
		t.Errorf("record[name] = %v, want %q", rec.Record["name"], "Widget A")
	}
	if _, ok := rec.Record["credit_limit"]; ok {
		t.Error("credit_limit (restrictive .Access() rule) should be excluded from the export entirely")
	}

	// Per-module staging objects are deliberately kept around (not
	// cleaned up by the worker itself) so a later re-run of the same job
	// can still re-assemble the archive without re-querying the database
	// — see TestWorkerRun_RetryAfterMarkCompleteSkipsAlreadyExportedModule.
	stagingKey := fmt.Sprintf("exports/%s/%d/modules/testmodule.jsonl", f.tenantID, jobID)
	if exists, err := f.worker.StorageBackend.Exists(ctx, stagingKey); err != nil || !exists {
		t.Errorf("expected per-module staging object to still exist (exists=%v, err=%v)", exists, err)
	}
}

func TestWorkerRun_RetryAfterMarkCompleteSkipsAlreadyExportedModule(t *testing.T) {
	f := newExportTestFixture(t)
	ctx := context.Background()
	jobID := time.Now().UnixNano()
	t.Cleanup(func() {
		_, _ = f.worker.RawDB.Exec("DELETE FROM system.job_checkpoints WHERE job_id = $1", fmt.Sprintf("%d", jobID))
	})
	args := Args{TenantID: f.tenantID, TenantSlug: f.tenantSlug}

	if _, err := f.worker.run(ctx, testJob(jobID, args)); err != nil {
		t.Fatalf("first run() error: %v", err)
	}

	// A second "attempt" of the same job (same job ID) should succeed by
	// skipping the already-complete module's lease acquisition, not by
	// re-querying data that's already been dumped and uploaded.
	result, err := f.worker.run(ctx, testJob(jobID, args))
	if err != nil {
		t.Fatalf("retry run() error: %v", err)
	}
	if result.DownloadURL == "" {
		t.Fatalf("incomplete result on retry: %+v", result)
	}
}
