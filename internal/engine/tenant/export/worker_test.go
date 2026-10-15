package tenantexport

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	crand "crypto/rand"
	"encoding/base64"
	"encoding/json/v2"
	"fmt"
	"slices"
	"testing"
	"time"
	"uuid"

	"github.com/djangbahevans/goerp/internal/engine/auth/membership/membershiptest"
	"github.com/djangbahevans/goerp/internal/engine/auth/rowcrypt"
	"github.com/djangbahevans/goerp/internal/engine/checkpoint"
	"github.com/djangbahevans/goerp/internal/engine/enginetables"
	enginemanifest "github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/registry"
	"github.com/djangbahevans/goerp/internal/engine/storage/storagetest"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	"github.com/djangbahevans/goerp/internal/engine/tenant/transfer"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
	"github.com/djangbahevans/goerp/sdk/go/model"
	"github.com/djangbahevans/goerp/sdk/go/perm"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
)

func widgetModelDecl() model.ModelDeclaration {
	return model.ModelDeclaration{
		Name:  "widget",
		Table: "widgets",
		Fields: []model.NamedField{
			{Name: "id", Def: model.UUID().Required().PrimaryKey()},
			{Name: "name", Def: model.Text().Required()},
			{Name: "credit_limit", Def: model.Integer().Access(model.AccessRead(perm.Ref("contacts:contact:financials_read")))},
		},
	}
}

type exportTestFixture struct {
	worker     *Worker
	tenantID   string
	tenantSlug string
	keys       *rowcrypt.RowKeySet
}

func testRowKeySet(t *testing.T) *rowcrypt.RowKeySet {
	t.Helper()
	key := make([]byte, 32)
	if _, err := crand.Read(key); err != nil {
		t.Fatalf("generate row encryption key: %v", err)
	}
	return &rowcrypt.RowKeySet{Active: rowcrypt.RowKey{KeyID: uuid.New().String(), Key: key}}
}

func newExportTestFixture(t *testing.T) *exportTestFixture {
	t.Helper()
	ctx := t.Context()
	conn := membershiptest.New(t)
	var database string
	if err := conn.QueryRowContext(ctx, "SELECT current_database()").Scan(&database); err != nil {
		t.Fatal(err)
	}
	conn = membershiptest.Open(t, database, "schema_sync_user")

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
	if err := tenantschema.Create(ctx, conn, slug); err != nil {
		t.Fatalf("create fixture schema: %v", err)
	}
	t.Cleanup(func() { _ = tenantschema.Drop(context.Background(), conn, slug) })
	if err := enginetables.CreateAll(ctx, conn, slug, []string{"en"}); err != nil {
		t.Fatal(err)
	}

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

	backend, err := storagetest.New(t)
	if err != nil {
		t.Fatalf(`storagetest.New(t) error: %v`, err)
	}

	keys := testRowKeySet(t)

	return &exportTestFixture{
		worker: &Worker{
			TenantStore:    tenantStore,
			Registry:       reg,
			RawDB:          conn,
			Checkpoints:    checkpointStore,
			StorageBackend: backend,
			Keys:           keys,
		},
		tenantID:   tt.ID,
		tenantSlug: tt.Slug,
		keys:       keys,
	}
}

func testJob(id int64, args Args) *river.Job[Args] {
	return &river.Job[Args]{JobRow: &rivertype.JobRow{ID: id, Attempt: 1}, Args: args}
}

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

func exportArchive(t *testing.T, f *exportTestFixture) (*zip.Reader, int64) {
	t.Helper()
	ctx := t.Context()
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

	archiveKeyB64, err := f.keys.Decrypt([]byte(result.DecryptionKey))
	if err != nil {
		t.Fatalf("Decrypt() error: %v", err)
	}

	plaintext := decryptArchive(t, ciphertext, string(archiveKeyB64))

	zr, err := zip.NewReader(bytes.NewReader(plaintext), int64(len(plaintext)))
	if err != nil {
		t.Fatalf("open decrypted archive as zip: %v", err)
	}
	return zr, jobID
}

func TestWorkerRun_ProducesDecryptableArchiveExcludingRestrictedField(t *testing.T) {
	f := newExportTestFixture(t)
	ctx := t.Context()
	zr, jobID := exportArchive(t, f)
	zf, err := zr.Open("testmodule.jsonl")
	if err != nil {
		t.Fatalf("open testmodule.jsonl: %v", err)
	}
	defer zf.Close()

	var rec exportRecord
	if err := json.UnmarshalRead(zf, &rec); err != nil {
		t.Fatalf("decode exported record: %v", err)
	}
	if rec.Record["name"] != "Widget A" {
		t.Errorf("record[name] = %v, want %q", rec.Record["name"], "Widget A")
	}
	if _, ok := rec.Record["credit_limit"]; ok {
		t.Error("credit_limit (restrictive .Access() rule) should be excluded from the export entirely")
	}

	stagingKey := fmt.Sprintf("exports/%s/%d/modules/testmodule.jsonl", f.tenantID, jobID)
	if exists, err := f.worker.StorageBackend.Exists(ctx, stagingKey); err != nil || !exists {
		t.Errorf("expected per-module staging object to still exist (exists=%v, err=%v)", exists, err)
	}
}

func TestWorkerRun_ArchiveHoldsEngineTablesWithoutInvitations(t *testing.T) {
	f := newExportTestFixture(t)
	ctx := t.Context()
	if _, err := f.worker.RawDB.ExecContext(ctx, fmt.Sprintf(
		`INSERT INTO %s.event_deliveries (subscriber_module, event_id, emitted_at, event_name, event_version)
   VALUES ('testmodule', uuidv7(), now(), 'testmodule.widget.created', 1)`, tenantschema.Name(f.tenantSlug))); err != nil {
		t.Fatal(err)
	}

	zr, _ := exportArchive(t, f)
	var names []string
	for _, file := range zr.File {
		names = append(names, file.Name)
	}
	for _, table := range transfer.Tables() {
		if !slices.Contains(names, table.File) {
			t.Errorf("missing %s", table.File)
		}
	}
	if slices.Contains(names, "engine/tenant_invitations.jsonl") {
		t.Error("archive contains invitations")
	}
	if !slices.Contains(names, "accounts.json") {
		t.Error("archive has no account list")
	}
}

func TestWorkerRun_RetryAfterMarkCompleteSkipsAlreadyExportedModule(t *testing.T) {
	f := newExportTestFixture(t)
	ctx := t.Context()
	jobID := time.Now().UnixNano()
	t.Cleanup(func() {
		_, _ = f.worker.RawDB.Exec("DELETE FROM system.job_checkpoints WHERE job_id = $1", fmt.Sprintf("%d", jobID))
	})
	args := Args{TenantID: f.tenantID, TenantSlug: f.tenantSlug}

	if _, err := f.worker.run(ctx, testJob(jobID, args)); err != nil {
		t.Fatalf("first run() error: %v", err)
	}

	result, err := f.worker.run(ctx, testJob(jobID, args))
	if err != nil {
		t.Fatalf("retry run() error: %v", err)
	}
	if result.DownloadURL == "" {
		t.Fatalf("incomplete result on retry: %+v", result)
	}
}

func TestWorkerRun_ModuleFiltersKeepEngineTables(t *testing.T) {
	f := newExportTestFixture(t)
	jobID := time.Now().UnixNano()
	_, err := f.worker.run(t.Context(), testJob(jobID, Args{TenantID: f.tenantID, TenantSlug: f.tenantSlug, Exclude: []string{"testmodule"}}))
	if err != nil {
		t.Fatal(err)
	}
	var tableCheckpoints int
	if err := f.worker.RawDB.QueryRowContext(t.Context(), "SELECT count(*) FROM system.job_checkpoints WHERE job_id = $1 AND module LIKE 'engine:%' AND status = 'complete'", fmt.Sprint(jobID)).Scan(&tableCheckpoints); err != nil {
		t.Fatal(err)
	}
	if tableCheckpoints != len(transfer.Tables()) {
		t.Fatalf("engine tables exported %d, want %d", tableCheckpoints, len(transfer.Tables()))
	}
}
