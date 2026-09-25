package wasm

import (
	"context"
	"database/sql"
	"encoding/json/v2"
	"fmt"
	"testing"
	"time"

	"github.com/vmihailenco/msgpack/v5"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/djangbahevans/goerp/internal/engine/dataaudit"
	"github.com/djangbahevans/goerp/internal/engine/fieldsec"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/permission"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

const (
	maskedWriteBankAccountPermission = "hr:employee:banking_read"
	maskedWriteUserID                = "00000000-0000-0000-0000-0000000000bb"
)

func maskedWriteModelDecl() model.ModelDeclaration {
	decl := fieldSecTestModelDecl()
	decl.Indexes = append(decl.Indexes, model.NamedIndex{Name: "idx_widgets_name_unique", Def: model.BTreeIndex("name").Unique()})
	return decl
}

func newMaskedWriteModuleContext(slug string, grantedPermissions ...string) *ModuleContext {
	decl := maskedWriteModelDecl()

	fieldSecReg := fieldsec.New()
	fieldSecReg.Register("testmodule", []model.ModelDeclaration{decl})

	permReg := permission.NewPermissionRegistry()
	permReg.Register("testmodule", []manifest.Permission{
		{Name: "contacts:contact:financials_read"},
		{Name: maskedWriteBankAccountPermission},
		{Name: "contacts:contact:notes_read"},
	})

	auditReg := dataaudit.New()
	auditReg.Register("testmodule", []manifest.AuditedTable{{Table: "widgets"}}, []model.ModelDeclaration{decl})

	var permSet permission.PermissionBitfield
	for _, name := range grantedPermissions {
		idx, ok := permReg.Index(name)
		if !ok {
			panic("newMaskedWriteModuleContext: unregistered permission " + name)
		}
		permSet.Set(idx)
	}

	// TenantID reuses the slug so each test's river_job event rows stay
	// isolated from every other test sharing the same database.
	return NewModuleContext("req-1", "testmodule", maskedWriteUserID, "contact-1", []string{"admin"}, permSet,
		slug, slug, "trace-1", abi.CapDBRead|abi.CapDBWrite, nil,
		ModuleSnapshot{
			ModelDecls:         []model.ModelDeclaration{decl},
			FieldSecRegistry:   fieldSecReg,
			PermissionRegistry: permReg,
			DataAuditRegistry:  auditReg,
		})
}

func setupMaskedWriteTenant(t *testing.T, primaryDB *sql.DB, prefix, existingID string) string {
	t.Helper()
	slug := fmt.Sprintf("%s%d", prefix, time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	createFieldSecFixtureTable(t, primaryDB, slug, existingID)
	createFixtureAuditLogTable(t, primaryDB, slug)
	return slug
}

func assertWidgetMaskedForDeniedCaller(t *testing.T, record map[string]any, wantBankAccountMask string) {
	t.Helper()
	if _, present := record["credit_limit"]; present {
		t.Errorf("credit_limit (Omit) should be absent from the returned record, got %v", record["credit_limit"])
	}
	if v, ok := record["bank_account"]; !ok || v != wantBankAccountMask {
		t.Errorf("bank_account (Mask) = %v, want %q", v, wantBankAccountMask)
	}
	if v, present := record["notes"]; !present {
		t.Error("notes (Nullify) should still be present as a key")
	} else if v != nil {
		t.Errorf("notes (Nullify) = %v, want nil", v)
	}
}

func assertStoredWidgetUnchanged(t *testing.T, primaryDB *sql.DB, slug, id string, wantCreditLimit int, wantBankAccount, wantNotes string) {
	t.Helper()
	var creditLimit int
	var bankAccount, notes string
	if err := primaryDB.QueryRow(
		"SELECT credit_limit, bank_account, notes FROM tenant_"+slug+".widgets WHERE id = $1", id,
	).Scan(&creditLimit, &bankAccount, &notes); err != nil {
		t.Fatalf("read stored row: %v", err)
	}
	if creditLimit != wantCreditLimit || bankAccount != wantBankAccount || notes != wantNotes {
		t.Errorf("stored row = (%d, %q, %q), want the unmasked (%d, %q, %q)",
			creditLimit, bankAccount, notes, wantCreditLimit, wantBankAccount, wantNotes)
	}
}

func latestEventRecord(t *testing.T, primaryDB *sql.DB, eventName, tenantID string) map[string]any {
	t.Helper()
	var body struct {
		Record map[string]any `msgpack:"record"`
	}
	if err := msgpack.Unmarshal(rawEventPayload(t, primaryDB, eventName, tenantID), &body); err != nil {
		t.Fatalf("unmarshal event payload: %v", err)
	}
	return body.Record
}

func assertEventRecordUnmasked(t *testing.T, record map[string]any, wantCreditLimit int64, wantBankAccount, wantNotes string) {
	t.Helper()
	if got := asInt(record["credit_limit"]); got != wantCreditLimit {
		t.Errorf("event credit_limit = %v, want the real value %d", record["credit_limit"], wantCreditLimit)
	}
	if record["bank_account"] != wantBankAccount {
		t.Errorf("event bank_account = %v, want the real value %q", record["bank_account"], wantBankAccount)
	}
	if record["notes"] != wantNotes {
		t.Errorf("event notes = %v, want the real value %q", record["notes"], wantNotes)
	}
}

func assertLatestAuditNewData(t *testing.T, primaryDB *sql.DB, slug, operation, id string, wantBankAccount string) {
	t.Helper()
	var found *auditLogRow
	for _, row := range queryAuditLogRows(t, primaryDB, slug, "widgets") {
		if row.Operation == operation && row.RecordID == id {
			found = &row
		}
	}
	if found == nil || !found.NewData.Valid {
		t.Fatalf("no %s audit row with new_data for %s", operation, id)
	}
	var newData map[string]any
	if err := json.Unmarshal([]byte(found.NewData.String), &newData); err != nil {
		t.Fatalf("unmarshal new_data: %v", err)
	}
	if newData["bank_account"] != wantBankAccount {
		t.Errorf("audit new_data bank_account = %v, want the real value %q", newData["bank_account"], wantBankAccount)
	}
	if _, present := newData["credit_limit"]; !present {
		t.Error("audit new_data should still carry credit_limit")
	}
}

func TestORMCreate_FieldSecurity_ReturnedRecordMasked(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := t.Context()

	slug := setupMaskedWriteTenant(t, primaryDB, "maskcreate", "11111111-1111-1111-1111-111111111111")
	r := newHostDBTestRuntime(t, primaryDB, 10)
	mc := newMaskedWriteModuleContext(slug)

	id := "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	out, hostErr := ORMCreate(ctx, r, primaryDB, r.EventInsertClient(), nil, mc, abiv1.ORMCreateInput{
		Model: "testmodule.widget",
		Record: map[string]any{
			"id": id, "name": "Widget B", "credit_limit": int64(900), "bank_account": "9876543210", "notes": "private",
		},
	})
	if hostErr != nil {
		t.Fatalf("ORMCreate: %+v", hostErr)
	}

	assertWidgetMaskedForDeniedCaller(t, out.Record, "****3210")
	if out.Record["name"] != "Widget B" {
		t.Errorf("name (no rule) = %v, want unaffected", out.Record["name"])
	}
	assertStoredWidgetUnchanged(t, primaryDB, slug, id, 900, "9876543210", "private")
	assertEventRecordUnmasked(t, latestEventRecord(t, primaryDB, "orm.record.created", slug), 900, "9876543210", "private")
	assertLatestAuditNewData(t, primaryDB, slug, "INSERT", id, "9876543210")
}

func TestORMCreate_FieldSecurity_GrantedPermissionReturnsRealValue(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := t.Context()

	slug := setupMaskedWriteTenant(t, primaryDB, "maskcreategranted", "11111111-1111-1111-1111-111111111111")
	r := newHostDBTestRuntime(t, primaryDB, 10)
	mc := newMaskedWriteModuleContext(slug, maskedWriteBankAccountPermission)

	out, hostErr := ORMCreate(ctx, r, primaryDB, r.EventInsertClient(), nil, mc, abiv1.ORMCreateInput{
		Model: "testmodule.widget",
		Record: map[string]any{
			"id": "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb", "name": "Widget C", "credit_limit": int64(900), "bank_account": "9876543210", "notes": "private",
		},
	})
	if hostErr != nil {
		t.Fatalf("ORMCreate: %+v", hostErr)
	}

	if out.Record["bank_account"] != "9876543210" {
		t.Errorf("bank_account = %v, want the real value for a caller holding the permission", out.Record["bank_account"])
	}
	if _, present := out.Record["credit_limit"]; present {
		t.Error("credit_limit should still be omitted: only the bank_account permission was granted")
	}
}

func TestORMWrite_FieldSecurity_ReturnedRecordMaskedIncludingUntouchedFields(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := t.Context()

	id := "11111111-1111-1111-1111-111111111111"
	slug := setupMaskedWriteTenant(t, primaryDB, "maskwrite", id)
	r := newHostDBTestRuntime(t, primaryDB, 10)
	mc := newMaskedWriteModuleContext(slug)

	out, hostErr := ORMWrite(ctx, r, primaryDB, r.EventInsertClient(), nil, mc, abiv1.ORMWriteInput{
		Model:  "testmodule.widget",
		ID:     id,
		Record: map[string]any{"name": "Widget A Renamed"},
	})
	if hostErr != nil {
		t.Fatalf("ORMWrite: %+v", hostErr)
	}

	assertWidgetMaskedForDeniedCaller(t, out.Record, "****7890")
	if out.Record["name"] != "Widget A Renamed" {
		t.Errorf("name = %v, want the written value", out.Record["name"])
	}
	assertStoredWidgetUnchanged(t, primaryDB, slug, id, 5000, "1234567890", "internal notes")
	assertEventRecordUnmasked(t, latestEventRecord(t, primaryDB, "orm.record.updated", slug), 5000, "1234567890", "internal notes")
	assertLatestAuditNewData(t, primaryDB, slug, "UPDATE", id, "1234567890")
}

func TestORMWrite_FieldSecurity_GrantedPermissionReturnsRealValue(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := t.Context()

	id := "11111111-1111-1111-1111-111111111111"
	slug := setupMaskedWriteTenant(t, primaryDB, "maskwritegranted", id)
	r := newHostDBTestRuntime(t, primaryDB, 10)
	mc := newMaskedWriteModuleContext(slug, maskedWriteBankAccountPermission)

	out, hostErr := ORMWrite(ctx, r, primaryDB, r.EventInsertClient(), nil, mc, abiv1.ORMWriteInput{
		Model:  "testmodule.widget",
		ID:     id,
		Record: map[string]any{"name": "Widget A Renamed"},
	})
	if hostErr != nil {
		t.Fatalf("ORMWrite: %+v", hostErr)
	}

	if out.Record["bank_account"] != "1234567890" {
		t.Errorf("bank_account = %v, want the real value for a caller holding the permission", out.Record["bank_account"])
	}
}

func TestORMCreateBatch_FieldSecurity_ReturnedRecordsMasked(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := t.Context()

	slug := setupMaskedWriteTenant(t, primaryDB, "maskbatch", "11111111-1111-1111-1111-111111111111")
	r := newHostDBTestRuntime(t, primaryDB, 10)
	mc := newMaskedWriteModuleContext(slug)

	firstID := "cccccccc-cccc-cccc-cccc-cccccccccccc"
	out, hostErr := ORMCreateBatch(ctx, r, primaryDB, r.EventInsertClient(), mc, abiv1.ORMCreateBatchInput{
		Model: "testmodule.widget",
		Records: []map[string]any{
			{"id": firstID, "name": "Widget D", "credit_limit": int64(1), "bank_account": "1111222233", "notes": "n1"},
			{"id": "dddddddd-dddd-dddd-dddd-dddddddddddd", "name": "Widget E", "credit_limit": int64(2), "bank_account": "4444555566", "notes": "n2"},
		},
	})
	if hostErr != nil {
		t.Fatalf("ORMCreateBatch: %+v", hostErr)
	}

	if len(out.Records) != 2 {
		t.Fatalf("got %d records, want 2", len(out.Records))
	}
	assertWidgetMaskedForDeniedCaller(t, out.Records[0], "****2233")
	assertWidgetMaskedForDeniedCaller(t, out.Records[1], "****5566")
	assertStoredWidgetUnchanged(t, primaryDB, slug, firstID, 1, "1111222233", "n1")
}

func TestORMFirstOrCreate_FieldSecurity_BothBranchesMasked(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := t.Context()

	existingID := "11111111-1111-1111-1111-111111111111"
	slug := setupMaskedWriteTenant(t, primaryDB, "maskfirstorcreate", existingID)
	r := newHostDBTestRuntime(t, primaryDB, 10)
	mc := newMaskedWriteModuleContext(slug)

	found, hostErr := ORMFirstOrCreate(ctx, r, primaryDB, r.EventInsertClient(), mc, abiv1.ORMFirstOrCreateInput{
		Model:      "testmodule.widget",
		UniqueVals: map[string]any{"name": "Widget A"},
	})
	if hostErr != nil {
		t.Fatalf("ORMFirstOrCreate (existing): %+v", hostErr)
	}
	if found.Created {
		t.Fatal("expected the existing row to be found, not created")
	}
	assertWidgetMaskedForDeniedCaller(t, found.Record, "****7890")
	assertStoredWidgetUnchanged(t, primaryDB, slug, existingID, 5000, "1234567890", "internal notes")

	createdID := "eeeeeeee-eeee-eeee-eeee-eeeeeeeeeeee"
	created, hostErr := ORMFirstOrCreate(ctx, r, primaryDB, r.EventInsertClient(), mc, abiv1.ORMFirstOrCreateInput{
		Model:      "testmodule.widget",
		UniqueVals: map[string]any{"name": "Widget F"},
		CreateVals: map[string]any{"id": createdID, "credit_limit": int64(7), "bank_account": "5555666677", "notes": "n3"},
	})
	if hostErr != nil {
		t.Fatalf("ORMFirstOrCreate (create): %+v", hostErr)
	}
	if !created.Created {
		t.Fatal("expected a new row to be created")
	}
	assertWidgetMaskedForDeniedCaller(t, created.Record, "****6677")
	assertStoredWidgetUnchanged(t, primaryDB, slug, createdID, 7, "5555666677", "n3")
	assertEventRecordUnmasked(t, latestEventRecord(t, primaryDB, "orm.record.created", slug), 7, "5555666677", "n3")
}

func maskedTransientModelDecl() model.ModelDeclaration {
	d := model.Define("wizard_item", model.Transient(time.Minute)).
		Field("id", model.UUID().PrimaryKey()).
		Field("name", model.Text().Required()).
		Field("etag", model.Text()).
		Field("secret", model.Text().
			Access(model.AccessRead("hr:employee:banking_read")).
			OnDeniedRead(model.Mask("****{last4}"))).
		Field("scratch", model.Text().
			Access(model.AccessRead("contacts:contact:notes_read")).
			OnDeniedRead(model.Omit))
	return *d
}

func TestORMTransient_FieldSecurity_CreateAndWriteReturnsMasked(t *testing.T) {
	ctx := t.Context()
	cacheClient := openTestCacheClient(t)
	primaryDB := openTestPrimaryDB(t)
	rt := newHostORMTransientTestRuntime(t, primaryDB, cacheClient)

	slug := fmt.Sprintf("masktransient%d", time.Now().UnixNano())
	decl := maskedTransientModelDecl()

	fieldSecReg := fieldsec.New()
	fieldSecReg.Register("testmodule", []model.ModelDeclaration{decl})
	permReg := permission.NewPermissionRegistry()
	permReg.Register("testmodule", []manifest.Permission{
		{Name: "hr:employee:banking_read"},
		{Name: "contacts:contact:notes_read"},
	})
	mc := NewModuleContext("req-1", "testmodule", maskedWriteUserID, "contact-1", []string{"admin"}, permission.PermissionBitfield{},
		slug, slug, "trace-1", abi.CapDBRead|abi.CapDBWrite, nil,
		ModuleSnapshot{
			ModelDecls:         []model.ModelDeclaration{decl},
			FieldSecRegistry:   fieldSecReg,
			PermissionRegistry: permReg,
		})

	created, hostErr := ORMCreate(ctx, rt, primaryDB, rt.EventInsertClient(), cacheClient, mc, abiv1.ORMCreateInput{
		Model:  "testmodule.wizard_item",
		Record: map[string]any{"name": "Step 1", "secret": "abcdef1234", "scratch": "tmp"},
	})
	if hostErr != nil {
		t.Fatalf("ORMCreate: %+v", hostErr)
	}
	id, _ := created.Record["id"].(string)
	t.Cleanup(func() { _ = cacheClient.Delete(context.Background(), transientKey(slug, "testmodule.wizard_item", id)) })

	if created.Record["secret"] != "****1234" {
		t.Errorf("created secret (Mask) = %v, want \"****1234\"", created.Record["secret"])
	}
	if _, present := created.Record["scratch"]; present {
		t.Errorf("created scratch (Omit) should be absent, got %v", created.Record["scratch"])
	}

	written, hostErr := ORMWrite(ctx, rt, primaryDB, rt.EventInsertClient(), cacheClient, mc, abiv1.ORMWriteInput{
		Model:  "testmodule.wizard_item",
		ID:     id,
		Record: map[string]any{"name": "Step 2", "secret": "zyxwvu9876"},
	})
	if hostErr != nil {
		t.Fatalf("ORMWrite: %+v", hostErr)
	}
	if written.Record["secret"] != "****9876" {
		t.Errorf("written secret (Mask) = %v, want \"****9876\"", written.Record["secret"])
	}
	if written.Record["name"] != "Step 2" {
		t.Errorf("written name = %v, want unaffected", written.Record["name"])
	}

	readOut, hostErr := ORMRead(ctx, primaryDB, cacheClient, mc, abiv1.ORMReadInput{Model: "testmodule.wizard_item", IDs: []string{id}})
	if hostErr != nil {
		t.Fatalf("ORMRead: %+v", hostErr)
	}
	if got := readOut.Records[0]["secret"]; got != "****9876" {
		t.Errorf("read secret = %v, want the masked \"****9876\"", got)
	}

	rawOut, hostErr := ORMRead(ctx, primaryDB, cacheClient, mc, abiv1.ORMReadInput{Model: "testmodule.wizard_item", IDs: []string{id}}, SkipFieldSecurity())
	if hostErr != nil {
		t.Fatalf("ORMRead (SkipFieldSecurity): %+v", hostErr)
	}
	if got := rawOut.Records[0]["secret"]; got != "zyxwvu9876" {
		t.Errorf("stored secret = %v, want the unmasked value", got)
	}
}
