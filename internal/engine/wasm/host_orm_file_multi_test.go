package wasm

import (
	"slices"
	"testing"
	"uuid"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/fieldsec"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/permission"
	"github.com/djangbahevans/goerp/sdk/go/model"
	"github.com/djangbahevans/goerp/sdk/go/orm"
	"github.com/djangbahevans/goerp/sdk/go/perm"
	"github.com/vmihailenco/msgpack/v5"
)

func (f *fileReferenceFixture) writeAttachments(t *testing.T, id string, record map[string]any) *abiv1.HostError {
	t.Helper()
	_, hostErr := ORMWrite(t.Context(), f.runtime, f.db, f.runtime.EventInsertClient(), nil, f.mc, abiv1.ORMWriteInput{Model: "testmodule.contract", ID: id, Record: record})
	return hostErr
}

func TestORMFileMultiRoundTrip(t *testing.T) {
	f := newFileReferenceFixture(t)
	out, hostErr := ORMCreate(t.Context(), f.runtime, f.db, f.runtime.EventInsertClient(), nil, f.mc, abiv1.ORMCreateInput{
		Model: "testmodule.contract", Record: map[string]any{"name": "Attachments", "attachment_ids": []string{f.privateID, f.publicID, f.privateID}},
	})
	if hostErr != nil {
		t.Fatal(hostErr)
	}
	id := out.Record["id"].(string)
	row := f.read(t, id, "attachment_ids")
	if _, present := row["id"]; present {
		t.Fatal("attachment projection exposes parent ID")
	}
	refs, err := orm.DecodeFileRefs(row["attachments"])
	if err != nil || len(refs) != 2 {
		t.Fatalf("attachments: %+v, %v", row, err)
	}
	for _, ref := range refs {
		if ref.Name != "contract.pdf" || ref.URL == "" || ref.SizeBytes != 3 || (ref.ID == f.privateID) != (ref.URLExpiresAt != nil) {
			t.Fatalf("file metadata: %+v", ref)
		}
	}

	if err := f.writeAttachments(t, id, map[string]any{"name": "Renamed"}); err != nil {
		t.Fatal(err)
	}
	if got := f.read(t, id); len(got["attachment_ids"].([]string)) != 2 {
		t.Fatalf("omitted attachments changed: %+v", got)
	}

	for _, ids := range [][]string{{f.publicID}, {}} {
		if err := f.writeAttachments(t, id, map[string]any{"attachment_ids": ids}); err != nil {
			t.Fatal(err)
		}
		got := f.read(t, id)
		if !slices.Equal(got["attachment_ids"].([]string), ids) || len(got["attachments"].([]map[string]any)) != len(ids) {
			t.Fatalf("replacement: %+v", got)
		}
	}

	if err := f.writeAttachments(t, id, map[string]any{"attachment_ids": []string{f.privateID}}); err != nil {
		t.Fatal(err)
	}
	caller := newHostORMCaller(t, t.Context(), f.runtime, f.mc)
	var searched abiv1.ORMSearchReadOutput
	env := callORMHost(t, t.Context(), caller, "call_search_read", abiv1.ORMSearchReadInput{Model: "testmodule.contract"}, &searched)
	if !env.OK || len(searched.Records) != 1 {
		t.Fatalf("search read: %+v, %+v", env.Error, searched)
	}
	if refs, err := orm.DecodeFileRefs(searched.Records[0]["attachments"]); err != nil || len(refs) != 1 || refs[0].ID != f.privateID {
		t.Fatalf("search attachments: %+v, %v", refs, err)
	}

	if _, err := f.db.ExecContext(t.Context(), "DELETE FROM tenant_"+f.mc.TenantSlug+".contracts WHERE id = $1", id); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := f.db.QueryRowContext(t.Context(), "SELECT count(*) FROM tenant_"+f.mc.TenantSlug+".contract_attachments").Scan(&count); err != nil || count != 0 {
		t.Fatalf("owner links remain: %d, %v", count, err)
	}
	if file, err := f.store.GetByID(t.Context(), f.mc.TenantSlug, f.privateID); err != nil || file.DeletedAt != nil {
		t.Fatalf("owner deletion changed file: %+v, %v", file, err)
	}
}

func TestORMFileMultiInvalidWritesAndInactiveReads(t *testing.T) {
	f := newFileReferenceFixture(t)
	id := f.create(t)
	if err := f.writeAttachments(t, id, map[string]any{"attachment_ids": []string{f.privateID}}); err != nil {
		t.Fatal(err)
	}
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

	for _, value := range []any{nil, "malformed", []any{42}, []string{"malformed"}, []string{uuid.NewV7().String()}, []string{foreignID}, []string{other.privateID}, []string{deletedID}, []string{quarantinedID}} {
		hostErr := f.writeAttachments(t, id, map[string]any{"name": "Changed", "attachment_ids": value})
		if hostErr == nil || hostErr.Code != abiv1.ErrCodeValidationFailed {
			t.Fatalf("accepted invalid attachments %v: %+v", value, hostErr)
		}
		if row := f.read(t, id); row["name"] != "Contract" || !slices.Equal(row["attachment_ids"].([]string), []string{f.privateID}) {
			t.Fatalf("failed replacement persisted: %+v", row)
		}
	}

	for _, badID := range []string{foreignID, deletedID, quarantinedID} {
		if _, err := f.db.ExecContext(t.Context(), "INSERT INTO tenant_"+f.mc.TenantSlug+".contract_attachments VALUES ($1, $2)", id, badID); err != nil {
			t.Fatal(err)
		}
	}
	f.backend.calls = 0
	row := f.read(t, id, "attachment_ids")
	if !slices.Equal(row["attachment_ids"].([]string), []string{f.privateID}) || f.backend.calls != 1 {
		t.Fatalf("inactive files exposed: %+v, URL calls=%d", row, f.backend.calls)
	}
}

func TestORMFileMultiFieldSecurityAndRequired(t *testing.T) {
	f := newFileReferenceFixture(t)
	id := f.create(t)
	if err := f.writeAttachments(t, id, map[string]any{"attachment_ids": []string{f.privateID}}); err != nil {
		t.Fatal(err)
	}
	secure := func(def model.FieldDef) {
		f.mc.snapshot.ModelDecls[0].Fields[4].Def = def
		fields := fieldsec.New()
		fields.Register("testmodule", f.mc.ModelDecls())
		permissions := permission.NewPermissionRegistry()
		permissions.Register("testmodule", []manifest.Permission{{Name: "testmodule:contract:file_read"}, {Name: "testmodule:contract:file_write"}})
		f.mc.snapshot.FieldSecRegistry = fields
		f.mc.snapshot.PermissionRegistry = permissions
	}
	for _, behavior := range []model.DeniedReadBehaviour{model.Omit, model.Nullify, model.Mask("hidden")} {
		secure(model.FileMulti("contract_attachments").Access(model.AccessRead(perm.Ref("testmodule:contract:file_read"))).OnDeniedRead(behavior))
		f.backend.calls = 0
		row := f.read(t, id, "attachment_ids")
		if f.backend.calls != 0 || row["attachments"] != nil {
			t.Fatalf("denied files signed: %+v", row)
		}
		if _, present := row["attachments"]; behavior.Kind == model.ReadKindOmit && present {
			t.Fatal("omitted expansion present")
		}
	}
	for _, behavior := range []model.DeniedWriteBehaviour{model.Reject, model.Ignore} {
		secure(model.FileMulti("contract_attachments").Access(model.AccessWrite(perm.Ref("testmodule:contract:file_write"))).OnDeniedWrite(behavior))
		err := f.writeAttachments(t, id, map[string]any{"name": "Changed", "attachment_ids": "malformed"})
		if behavior == model.Reject && (err == nil || err.Code != abiv1.ErrCodeFieldWriteDenied) || behavior == model.Ignore && err != nil {
			t.Fatalf("denied write: %+v", err)
		}
		if got := f.read(t, id); !slices.Equal(got["attachment_ids"].([]string), []string{f.privateID}) {
			t.Fatalf("denied write changed links: %+v", got)
		}
	}

	secure(model.FileMulti("contract_attachments").Required())
	if err := f.writeAttachments(t, id, map[string]any{"attachment_ids": []string{}}); err == nil {
		t.Fatal("required set cleared")
	}
	if _, err := ORMCreate(t.Context(), f.runtime, f.db, f.runtime.EventInsertClient(), nil, f.mc, abiv1.ORMCreateInput{Model: "testmodule.contract", Record: map[string]any{"name": "Missing"}}); err == nil {
		t.Fatal("required set omitted")
	}
	secure(model.FileMulti("contract_attachments").Readonly())
	if err := f.writeAttachments(t, id, map[string]any{"attachment_ids": []string{f.publicID}}); err == nil || err.Code != abiv1.ErrCodeFieldNotWritable {
		t.Fatalf("readonly attachments: %+v", err)
	}
}

func TestORMFileMultiBatchRollbackAndSoftDelete(t *testing.T) {
	f := newFileReferenceFixture(t)
	caller := newHostORMWriteCaller(t, t.Context(), f.runtime, f.mc)
	env := callORMHost(t, t.Context(), caller, "call_create_batch", abiv1.ORMCreateBatchInput{
		Model: "testmodule.contract", Records: []map[string]any{
			{"name": "Valid", "attachment_ids": []string{f.privateID}},
			{"name": "Invalid", "attachment_ids": []string{uuid.NewV7().String()}},
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
	if err := f.writeAttachments(t, id, map[string]any{"attachment_ids": []string{f.privateID}}); err != nil {
		t.Fatal(err)
	}
	f.runtime.txLimiter = NewTransactionLimiter(10)
	f.mc.txLimiter = f.runtime.TxLimiter()
	dbCaller := newHostDBCaller(t, t.Context(), f.runtime, f.mc)
	begun := callHost(t, t.Context(), dbCaller, "call_begin", abiv1.DBBeginInput{})
	if !begun.OK {
		t.Fatal(begun.Error)
	}
	var transaction abiv1.DBBeginOutput
	if err := msgpack.Unmarshal(begun.Data, &transaction); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.mc.RollbackAll() })
	_, hostErr := ORMWrite(t.Context(), f.runtime, f.db, f.runtime.EventInsertClient(), nil, f.mc, abiv1.ORMWriteInput{Model: "testmodule.contract", ID: id, Record: map[string]any{"attachment_ids": []string{f.publicID}}, TxID: transaction.TxID})
	if hostErr != nil {
		t.Fatal(hostErr)
	}
	out, hostErr := ORMRead(t.Context(), f.db, nil, f.mc, abiv1.ORMReadInput{Model: "testmodule.contract", IDs: []string{id}, TxID: transaction.TxID})
	if hostErr != nil || !slices.Equal(out.Records[0]["attachment_ids"].([]string), []string{f.publicID}) {
		t.Fatalf("transaction read: %+v, %+v", out, hostErr)
	}
	rolledBack := callHost(t, t.Context(), dbCaller, "call_rollback", abiv1.DBTxIDInput{TxID: transaction.TxID})
	if !rolledBack.OK {
		t.Fatal(rolledBack.Error)
	}
	if got := f.read(t, id); !slices.Equal(got["attachment_ids"].([]string), []string{f.privateID}) {
		t.Fatalf("replacement survived rollback: %+v", got)
	}

	if _, err := f.db.ExecContext(t.Context(), "ALTER TABLE tenant_"+f.mc.TenantSlug+".contracts ADD COLUMN deleted_at timestamptz"); err != nil {
		t.Fatal(err)
	}
	f.mc.snapshot.ModelDecls[0].Fields = append(f.mc.snapshot.ModelDecls[0].Fields, model.NamedField{Name: "deleted_at", Def: model.TimestampTZ().Readonly()})
	unlink := newHostORMWriteCaller(t, t.Context(), f.runtime, f.mc)
	env = callORMHost(t, t.Context(), unlink, "call_unlink", abiv1.ORMUnlinkInput{Model: "testmodule.contract", IDs: []string{id}}, nil)
	if !env.OK {
		t.Fatal(env.Error)
	}
	if err := f.db.QueryRowContext(t.Context(), "SELECT count(*) FROM tenant_"+f.mc.TenantSlug+".contract_attachments WHERE contract_id = $1", id).Scan(&count); err != nil || count != 1 {
		t.Fatalf("soft deletion removed links: %d, %v", count, err)
	}
	if _, err := f.db.ExecContext(t.Context(), "UPDATE tenant_"+f.mc.TenantSlug+".contracts SET deleted_at = NULL WHERE id = $1", id); err != nil {
		t.Fatal(err)
	}
	if got := f.read(t, id); !slices.Equal(got["attachment_ids"].([]string), []string{f.privateID}) {
		t.Fatalf("restored attachments: %+v", got)
	}
}

func TestORMFileMultiRLS(t *testing.T) {
	f := newFileReferenceFixture(t)
	id := f.create(t)
	if err := f.writeAttachments(t, id, map[string]any{"attachment_ids": []string{f.privateID}}); err != nil {
		t.Fatal(err)
	}
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
	row := f.read(t, id, "attachment_ids")
	if len(row["attachment_ids"].([]string)) != 0 || len(row["attachments"].([]map[string]any)) != 0 || f.backend.calls != 0 {
		t.Fatalf("RLS-hidden files exposed: %+v", row)
	}
	if err := f.writeAttachments(t, id, map[string]any{"attachment_ids": []string{f.publicID}}); err == nil {
		t.Fatal("RLS-hidden file linked")
	}
}
