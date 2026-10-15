package wasm

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"
	"uuid"

	"ariga.io/atlas/sql/postgres"
	atlasschema "ariga.io/atlas/sql/schema"
	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/djangbahevans/goerp/internal/engine/fieldsec"
	"github.com/djangbahevans/goerp/internal/engine/files"
	"github.com/djangbahevans/goerp/internal/engine/jobqueue"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/permission"
	engineschema "github.com/djangbahevans/goerp/internal/engine/schema"
	"github.com/djangbahevans/goerp/internal/engine/storage"
	"github.com/djangbahevans/goerp/sdk/go/model"
	"github.com/djangbahevans/goerp/sdk/go/orm"
	"github.com/djangbahevans/goerp/sdk/go/perm"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/vmihailenco/msgpack/v5"
)

type fileReferenceBackend struct {
	storage.Backend
	calls  int
	expiry time.Duration
}

func (b *fileReferenceBackend) SignedURL(ctx context.Context, key string, expiry time.Duration) (string, error) {
	b.calls++
	b.expiry = expiry
	return b.Backend.SignedURL(ctx, key, expiry)
}

func (b *fileReferenceBackend) PublicURL(ctx context.Context, key string) (string, error) {
	b.calls++
	return b.Backend.PublicURL(ctx, key)
}

type fileReferenceFixture struct {
	db        *sql.DB
	runtime   *Runtime
	mc        *ModuleContext
	store     *files.Store
	backend   *fileReferenceBackend
	privateID string
	publicID  string
}

func newFileReferenceFixture(t *testing.T) *fileReferenceFixture {
	t.Helper()
	db := openTestPrimaryDB(t)
	pool, err := pgxpool.New(t.Context(), localPostgresDSN)
	if err != nil {
		t.Fatal(err)
	}
	if err := jobqueue.Migrate(t.Context(), pool); err != nil {
		pool.Close()
		t.Fatal(err)
	}
	pool.Close()

	slug := fmt.Sprintf("fileref%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, db, slug)

	store := files.NewStore(db)
	if err := store.Bootstrap(t.Context(), slug); err != nil {
		t.Fatal(err)
	}

	md := model.Define("contract", model.Table("contracts")).
		Field("id", model.UUID().PrimaryKey().Default("uuidv7()")).
		Field("name", model.Text().Required()).
		Field("signed_pdf_id", model.File()).
		Field("avatar_id", model.File())
	declared, err := engineschema.ToAtlasSchema("tenant_"+slug, "testmodule", []model.ModelDeclaration{*md}, nil)
	if err != nil {
		t.Fatal(err)
	}

	driver, err := postgres.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := driver.PlanChanges(t.Context(), "file", []atlasschema.Change{&atlasschema.AddTable{T: declared.Tables[0]}})
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range plan.Changes {
		if _, err := db.ExecContext(t.Context(), change.Cmd); err != nil {
			t.Fatal(err)
		}
	}
	grantFixtureTables(t, db, slug, "contracts", "files")

	backend := &fileReferenceBackend{Backend: newTestLocalBackend(t)}
	rt := newHostStorageTestRuntime(t, db, backend)
	mc := NewModuleContext("req", "testmodule", "user", "contact", nil, nil, uuid.NewV7().String(), slug, "trace", abi.CapDBRead|abi.CapDBWrite, rt.TxLimiter(), ModuleSnapshot{
		ModelDecls: []model.ModelDeclaration{*md},
		FileURL:    rt.FileURL,
	})
	f := &fileReferenceFixture{db: db, runtime: rt, mc: mc, store: store, backend: backend}
	f.privateID = f.upload(t, false, mc.TenantID)
	f.publicID = f.upload(t, true, mc.TenantID)
	t.Cleanup(func() {
		if _, err := db.ExecContext(context.Background(), "DELETE FROM system.river_job WHERE args->>'tenant_id' = $1", mc.TenantID); err != nil {
			t.Errorf("delete fixture events: %v", err)
		}
	})

	return f
}

func (f *fileReferenceFixture) upload(t *testing.T, public bool, tenantID string) string {
	t.Helper()
	id := uuid.NewV7().String()
	key := "attachments/" + tenantID + "/" + id + ".pdf"
	if _, err := f.backend.Upload(t.Context(), key, bytes.NewReader([]byte("pdf")), storage.UploadOptions{ContentType: "application/pdf", Public: public}); err != nil {
		t.Fatal(err)
	}

	if err := f.store.Insert(t.Context(), f.mc.TenantSlug, files.InsertRow{
		ID:             id,
		TenantID:       tenantID,
		StorageKey:     key,
		OriginalName:   "contract.pdf",
		ContentType:    "application/pdf",
		SizeBytes:      3,
		ChecksumSHA256: "checksum",
		Purpose:        "attachments",
		IsPublic:       public,
	}); err != nil {
		t.Fatal(err)
	}
	return id
}

func (f *fileReferenceFixture) create(t *testing.T) string {
	t.Helper()
	caller := newHostORMWriteCaller(t, t.Context(), f.runtime, f.mc)
	var output abiv1.ORMCreateOutput
	env := callORMHost(t, t.Context(), caller, "call_create", abiv1.ORMCreateInput{
		Model: "testmodule.contract", Record: map[string]any{"name": "Contract", "signed_pdf_id": f.privateID, "avatar_id": f.publicID},
	}, &output)
	if !env.OK {
		t.Fatalf("create: %+v", env.Error)
	}
	return output.Record["id"].(string)
}

func (f *fileReferenceFixture) read(t *testing.T, id string, fields ...string) map[string]any {
	t.Helper()
	out, hostErr := ORMRead(t.Context(), f.db, nil, f.mc, abiv1.ORMReadInput{Model: "testmodule.contract", IDs: []string{id}, Fields: fields})
	if hostErr != nil || len(out.Records) != 1 {
		t.Fatalf("read: %+v, %+v", hostErr, out)
	}
	return out.Records[0]
}

func (f *fileReferenceFixture) secure(def model.FieldDef) {
	f.mc.snapshot.ModelDecls[0].Fields[2].Def = def
	fields := fieldsec.New()
	fields.Register("testmodule", f.mc.ModelDecls())
	permissions := permission.NewPermissionRegistry()
	permissions.Register("testmodule", []manifest.Permission{{Name: "testmodule:contract:file_read"}, {Name: "testmodule:contract:file_write"}})
	f.mc.snapshot.FieldSecRegistry = fields
	f.mc.snapshot.PermissionRegistry = permissions
}

func TestORMFileRoundTrip(t *testing.T) {
	f := newFileReferenceFixture(t)
	id := f.create(t)
	before := time.Now().UTC().Add(time.Hour)
	row := f.read(t, id, "signed_pdf_id", "avatar_id")
	if _, found := row["id"]; found {
		t.Fatal("file-only projection leaked parent ID")
	}

	private, err := orm.DecodeFileRef(row["signed_pdf"])
	if err != nil {
		t.Fatal(err)
	}
	if private == nil || private.ID != f.privateID || private.Name != "contract.pdf" || private.ContentType != "application/pdf" || private.SizeBytes != 3 {
		t.Fatalf("private metadata: %+v", private)
	}
	if private.URL == "" || private.URLExpiresAt == nil || private.URLExpiresAt.Before(before.Add(-time.Second)) || private.URLExpiresAt.After(time.Now().Add(time.Hour)) || f.backend.expiry != time.Hour {
		t.Fatalf("private file: %+v, expiry: %v", private, f.backend.expiry)
	}
	public := row["avatar"].(map[string]any)
	if row["signed_pdf_id"] != f.privateID || public["id"] != f.publicID || public["url"] == "" || public["url_expires_at"] != nil {
		t.Fatalf("file response: %+v", row)
	}

	caller := newHostORMCaller(t, t.Context(), f.runtime, f.mc)
	var searched abiv1.ORMSearchReadOutput
	env := callORMHost(t, t.Context(), caller, "call_search_read", abiv1.ORMSearchReadInput{Model: "testmodule.contract"}, &searched)
	if !env.OK || len(searched.Records) != 1 || searched.Records[0]["signed_pdf"] == nil {
		t.Fatalf("search read: %+v, %+v", env.Error, searched)
	}
	if _, err := orm.DecodeFileRef(searched.Records[0]["signed_pdf"]); err != nil {
		t.Fatal(err)
	}

	for _, record := range []map[string]any{{"name": "Renamed"}, {"signed_pdf_id": nil}, {"signed_pdf_id": f.publicID}} {
		if _, hostErr := ORMWrite(t.Context(), f.runtime, f.db, f.runtime.EventInsertClient(), nil, f.mc, abiv1.ORMWriteInput{Model: "testmodule.contract", ID: id, Record: record}); hostErr != nil {
			t.Fatalf("write: %+v", hostErr)
		}

		got := f.read(t, id)
		expected := any(f.privateID)
		if value, present := record["signed_pdf_id"]; present {
			expected = value
		}
		if got["signed_pdf_id"] != expected || (expected == nil && got["signed_pdf"] != nil) {
			t.Fatalf("omit/clear/replace: %+v", got)
		}
	}

	unlink := newHostORMWriteCaller(t, t.Context(), f.runtime, f.mc)
	env = callORMHost(t, t.Context(), unlink, "call_unlink", abiv1.ORMUnlinkInput{Model: "testmodule.contract", IDs: []string{id}}, nil)
	if !env.OK {
		t.Fatalf("unlink: %+v", env.Error)
	}
	file, err := f.store.GetByID(t.Context(), f.mc.TenantSlug, f.publicID)
	if err != nil || file.DeletedAt != nil {
		t.Fatalf("owning deletion changed file: %+v, %v", file, err)
	}
}

func TestORMFileRejectsInvalidReferencesAtomically(t *testing.T) {
	f := newFileReferenceFixture(t)
	id := f.create(t)
	deletedID := f.upload(t, false, f.mc.TenantID)
	if err := f.store.MarkDeleted(t.Context(), f.mc.TenantSlug, deletedID); err != nil {
		t.Fatal(err)
	}
	foreignID := f.upload(t, false, uuid.NewV7().String())
	other := newFileReferenceFixture(t)
	quarantinedID := f.upload(t, false, f.mc.TenantID)
	if _, err := f.db.ExecContext(t.Context(), "UPDATE tenant_"+f.mc.TenantSlug+".files SET scan_status = 'quarantined' WHERE id = $1", quarantinedID); err != nil {
		t.Fatal(err)
	}

	for _, value := range []any{"malformed", uuid.NewV7().String(), foreignID, other.privateID, deletedID, quarantinedID, map[string]any{"id": f.privateID}} {
		_, hostErr := ORMWrite(t.Context(), f.runtime, f.db, f.runtime.EventInsertClient(), nil, f.mc, abiv1.ORMWriteInput{Model: "testmodule.contract", ID: id, Record: map[string]any{"name": "Changed", "signed_pdf_id": value}})
		if hostErr == nil || hostErr.Code != abiv1.ErrCodeValidationFailed {
			t.Fatalf("accepted invalid file %v: %+v", value, hostErr)
		}
		if row := f.read(t, id); row["name"] != "Contract" || row["signed_pdf_id"] != f.privateID {
			t.Fatalf("failed write changed record: %+v", row)
		}
	}

	for _, badID := range []string{foreignID, deletedID, quarantinedID} {
		if _, err := f.db.ExecContext(t.Context(), "UPDATE tenant_"+f.mc.TenantSlug+".contracts SET signed_pdf_id = $1 WHERE id = $2", badID, id); err != nil {
			t.Fatal(err)
		}

		f.backend.calls = 0
		row := f.read(t, id, "signed_pdf_id")
		if row["signed_pdf"] != nil || row["signed_pdf_id"] != badID || f.backend.calls != 0 {
			t.Fatalf("unavailable reference disclosed metadata: %+v", row)
		}
	}
}

func TestORMFileFieldSecurityAndRequired(t *testing.T) {
	f := newFileReferenceFixture(t)
	id := f.create(t)
	for _, behavior := range []model.DeniedReadBehaviour{model.Omit, model.Nullify, model.Mask("hidden")} {
		f.secure(model.File().Access(model.AccessRead(perm.Ref("testmodule:contract:file_read"))).OnDeniedRead(behavior))
		f.backend.calls = 0
		row := f.read(t, id, "signed_pdf_id")
		if f.backend.calls != 0 || row["signed_pdf"] != nil {
			t.Fatalf("denied file signed or expanded: %+v", row)
		}
		if _, found := row["signed_pdf"]; behavior.Kind == model.ReadKindOmit && found {
			t.Fatal("omitted file expansion leaked")
		}
	}

	for _, behavior := range []model.DeniedWriteBehaviour{model.Reject, model.Ignore} {
		f.secure(model.File().Access(model.AccessWrite(perm.Ref("testmodule:contract:file_write"))).OnDeniedWrite(behavior))
		_, hostErr := ORMWrite(t.Context(), f.runtime, f.db, f.runtime.EventInsertClient(), nil, f.mc, abiv1.ORMWriteInput{Model: "testmodule.contract", ID: id, Record: map[string]any{"name": "Changed", "signed_pdf_id": "malformed"}})
		if behavior == model.Reject && (hostErr == nil || hostErr.Code != abiv1.ErrCodeFieldWriteDenied) {
			t.Fatalf("write restriction: %+v", hostErr)
		}
		if behavior == model.Ignore && hostErr != nil {
			t.Fatalf("ignored file validated: %+v", hostErr)
		}
		if row := f.read(t, id); row["signed_pdf_id"] != f.privateID {
			t.Fatal("denied write changed file")
		}
	}

	f.secure(model.File().Required())
	if _, hostErr := ORMCreate(t.Context(), f.runtime, f.db, f.runtime.EventInsertClient(), nil, f.mc, abiv1.ORMCreateInput{Model: "testmodule.contract", Record: map[string]any{"name": "Missing"}}); hostErr == nil {
		t.Fatal("required file missing on create")
	}
	if _, hostErr := ORMWrite(t.Context(), f.runtime, f.db, f.runtime.EventInsertClient(), nil, f.mc, abiv1.ORMWriteInput{Model: "testmodule.contract", ID: id, Record: map[string]any{"signed_pdf_id": nil}}); hostErr == nil {
		t.Fatal("required file cleared")
	}

	f.secure(model.File().Readonly())
	for _, fileID := range []string{f.publicID, "malformed"} {
		if _, hostErr := ORMWrite(t.Context(), f.runtime, f.db, f.runtime.EventInsertClient(), nil, f.mc, abiv1.ORMWriteInput{Model: "testmodule.contract", ID: id, Record: map[string]any{"signed_pdf_id": fileID}}); hostErr == nil || hostErr.Code != abiv1.ErrCodeFieldNotWritable {
			t.Fatalf("readonly file: %+v", hostErr)
		}
	}
}

func TestORMFileBatchAndTransactionRollback(t *testing.T) {
	f := newFileReferenceFixture(t)
	caller := newHostORMWriteCaller(t, t.Context(), f.runtime, f.mc)
	env := callORMHost(t, t.Context(), caller, "call_create_batch", abiv1.ORMCreateBatchInput{
		Model: "testmodule.contract", Records: []map[string]any{
			{"name": "Valid", "signed_pdf_id": f.privateID},
			{"name": "Invalid", "signed_pdf_id": uuid.NewV7().String()},
		},
	}, nil)
	if env.OK {
		t.Fatal("invalid batch accepted")
	}

	var count int
	if err := f.db.QueryRowContext(t.Context(), "SELECT count(*) FROM tenant_"+f.mc.TenantSlug+".contracts").Scan(&count); err != nil || count != 0 {
		t.Fatalf("batch partially persisted: %d, %v", count, err)
	}

	id := f.create(t)
	f.runtime.txLimiter = NewTransactionLimiter(10)
	f.mc.txLimiter = f.runtime.TxLimiter()
	dbCaller := newHostDBCaller(t, t.Context(), f.runtime, f.mc)
	begun := callHost(t, t.Context(), dbCaller, "call_begin", abiv1.DBBeginInput{})
	if !begun.OK {
		t.Fatalf("begin: %+v", begun.Error)
	}
	var transaction abiv1.DBBeginOutput
	if err := msgpack.Unmarshal(begun.Data, &transaction); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.mc.RollbackAll() })

	_, hostErr := ORMWrite(t.Context(), f.runtime, f.db, f.runtime.EventInsertClient(), nil, f.mc, abiv1.ORMWriteInput{
		Model: "testmodule.contract", ID: id, Record: map[string]any{"signed_pdf_id": f.publicID}, TxID: transaction.TxID,
	})
	if hostErr != nil {
		t.Fatalf("write in transaction: %+v", hostErr)
	}
	out, hostErr := ORMRead(t.Context(), f.db, nil, f.mc, abiv1.ORMReadInput{Model: "testmodule.contract", IDs: []string{id}, TxID: transaction.TxID})
	if hostErr != nil || out.Records[0]["signed_pdf_id"] != f.publicID {
		t.Fatalf("read own file write: %+v, %+v", hostErr, out)
	}

	rolledBack := callHost(t, t.Context(), dbCaller, "call_rollback", abiv1.DBTxIDInput{TxID: transaction.TxID})
	if !rolledBack.OK {
		t.Fatalf("rollback: %+v", rolledBack.Error)
	}
	if got := f.read(t, id); got["signed_pdf_id"] != f.privateID {
		t.Fatalf("file write survived rollback: %+v", got)
	}
}

func TestORMFileRLSAndMissingTargets(t *testing.T) {
	f := newFileReferenceFixture(t)
	id := f.create(t)
	schema := "tenant_" + f.mc.TenantSlug
	if _, err := f.db.ExecContext(t.Context(), "ALTER TABLE "+schema+".files ENABLE ROW LEVEL SECURITY"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(t.Context(), "CREATE POLICY visible ON "+schema+".files USING (false)"); err != nil {
		t.Fatal(err)
	}

	reader := openTestRLSReaderORM(t, f.db, schema, schema+".files")
	if _, err := f.db.ExecContext(t.Context(), "GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA "+schema+" TO goerp_test_rls_reader_orm"); err != nil {
		t.Fatal(err)
	}
	f.db = reader
	f.backend.calls = 0
	if got := f.read(t, id, "signed_pdf_id"); got["signed_pdf"] != nil || f.backend.calls != 0 {
		t.Fatalf("RLS-hidden file exposed: %+v", got)
	}
	if _, hostErr := ORMWrite(t.Context(), f.runtime, f.db, f.runtime.EventInsertClient(), nil, f.mc, abiv1.ORMWriteInput{Model: "testmodule.contract", ID: id, Record: map[string]any{"signed_pdf_id": f.publicID}}); hostErr == nil {
		t.Fatal("RLS-hidden file linked")
	}

	owner := openTestPrimaryDB(t)
	if _, err := owner.ExecContext(t.Context(), "ALTER TABLE "+schema+".contracts DROP CONSTRAINT contracts_signed_pdf_id_fkey"); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.ExecContext(t.Context(), "UPDATE "+schema+".contracts SET signed_pdf_id = uuidv7() WHERE id = $1", id); err != nil {
		t.Fatal(err)
	}
	f.db = owner
	f.backend.calls = 0
	if got := f.read(t, id, "signed_pdf_id"); got["signed_pdf"] != nil || f.backend.calls != 0 {
		t.Fatalf("missing file exposed: %+v", got)
	}
}
