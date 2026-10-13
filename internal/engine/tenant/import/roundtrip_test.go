package tenantimport

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"
	"uuid"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/checkpoint"
	"github.com/djangbahevans/goerp/internal/engine/files"
	"github.com/djangbahevans/goerp/internal/engine/invite"
	"github.com/djangbahevans/goerp/internal/engine/jobqueue"
	"github.com/djangbahevans/goerp/internal/engine/role"
	"github.com/djangbahevans/goerp/internal/engine/storage"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	tenantexport "github.com/djangbahevans/goerp/internal/engine/tenant/export"
	"github.com/djangbahevans/goerp/internal/engine/tenant/transfer"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
	"github.com/djangbahevans/goerp/internal/engine/user"
	"github.com/djangbahevans/goerp/sdk/go/model"
	"github.com/djangbahevans/goerp/sdk/go/perm"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivertest"
)

type interruptedStorage struct {
	storage.Backend
	failKey string
	uploads map[string]int
}

func (s *interruptedStorage) Upload(ctx context.Context, key string, r io.Reader, opts storage.UploadOptions) (string, error) {
	s.uploads[key]++
	if key == s.failKey {
		s.failKey = ""
		return "", errors.New("interrupted upload")
	}
	return s.Backend.Upload(ctx, key, r, opts)
}

type importMailer struct {
	token  string
	emails []string
	fail   bool
}

func (m *importMailer) SendInvite(_ context.Context, email, _, token string, isNew bool) error {
	if !isNew {
		return errors.New("restored account should need a password")
	}
	if m.fail {
		m.fail = false
		return errors.New("mail unavailable")
	}
	m.token = token
	m.emails = append(m.emails, email)
	return nil
}

func execSQL(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.ExecContext(t.Context(), query, args...); err != nil {
		t.Fatal(err)
	}
}

func TestTenantTransfer_RoundTripAndRetriesPreserveMembershipFilesAndAccountCounts(t *testing.T) {
	f := newImportTestFixture(t)
	ctx := t.Context()
	md := widgetModelDecl()
	md.Fields = append(md.Fields,
		model.NamedField{Name: "tenant_id", Def: model.UUID()},
		model.NamedField{Name: "created_by", Def: model.UUID()},
		model.NamedField{Name: "updated_by", Def: model.UUID()},
		model.NamedField{Name: "binary_data", Def: model.Bytea()},
		model.NamedField{Name: "metadata", Def: model.JSONB()},
		model.NamedField{Name: "private", Def: model.Text().Access(model.AccessRead(perm.Ref("testmodule:widget:private")))},
	)
	modules := f.reg.Snapshot().Modules()
	modules["testmodule"].ModelDecls = []model.ModelDeclaration{md}
	if _, err := f.reg.Update(modules); err != nil {
		t.Fatal(err)
	}

	source, err := f.worker.TenantStore.CreateTenant(ctx, "transfersource", "Source")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.worker.Provision.CreateTenantSchema(ctx, source.Slug); err != nil {
		t.Fatal(err)
	}
	if err := f.worker.Provision.CreateEngineTables(ctx, source.Slug); err != nil {
		t.Fatal(err)
	}
	if err := f.worker.Provision.SyncModuleSchema(ctx, source.ID, source.Slug, "testmodule", abiv1.Request{}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tenantschema.Drop(context.Background(), f.conn, source.Slug) })
	schema := tenantschema.Name(source.Slug)
	users := user.NewStore(f.conn)
	oldMatched, err := users.FindOrCreateInvited(ctx, "matched@transfer.test")
	if err != nil {
		t.Fatal(err)
	}
	oldCreated, err := users.FindOrCreateInvited(ctx, "created@transfer.test")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range []struct{ ID, Name string }{{oldMatched, "Matched Source"}, {oldCreated, "New Member"}} {
		if err := users.EnsureProfile(ctx, entry.ID, entry.Name); err != nil {
			t.Fatal(err)
		}
	}
	if err := role.NewStore(f.conn).SeedBuiltinRoles(ctx, source.Slug); err != nil {
		t.Fatal(err)
	}
	roleID, contactID, widgetID := uuid.NewV7().String(), uuid.NewV7().String(), uuid.NewV7().String()
	execSQL(t, f.conn, "INSERT INTO "+schema+".roles (id, name) VALUES ($1, 'reviewer')", roleID)
	execSQL(t, f.conn, "INSERT INTO "+schema+".role_permissions (role_id, permission_name, granted_by) VALUES ($1, 'testmodule:widget:read', $2)", roleID, oldMatched)
	execSQL(t, f.conn, "INSERT INTO "+schema+".tenant_members (user_id, status, phone, job_title, contact_id, suspended_by) VALUES ($1, 'suspended', '+233201234567', 'Auditor', $2, $3), ($3, 'active', NULL, NULL, NULL, NULL)", oldCreated, contactID, oldMatched)
	grantedAt := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	expiresAt := time.Date(2027, 9, 1, 12, 0, 0, 0, time.UTC)
	execSQL(t, f.conn, "INSERT INTO "+schema+".user_roles (user_id, role_id, granted_by, granted_at, expires_at) VALUES ($1, $2, $3, $4, $5), ($3, $2, $3, $4, $5)", oldCreated, roleID, oldMatched, grantedAt, expiresAt)
	execSQL(t, f.conn, "INSERT INTO "+schema+".tenant_invitations (email, role_id, token_hash, expires_at) VALUES ('pending@transfer.test', $1, 'secret-invite-hash', NOW()+INTERVAL '7 days')", roleID)
	execSQL(t, f.conn, "INSERT INTO "+schema+".module_config (module_name, key, value, value_type, encrypted) VALUES ('testmodule', 'secret', '\"secret-config\"', 'string', true), ('testmodule', 'label', '\"portable-label\"', 'string', false)")
	execSQL(t, f.conn, "INSERT INTO "+schema+".user_device_tokens (tenant_id, user_id, platform, token) VALUES ($1, $2, 'web', 'secret-push-token')", source.ID, oldMatched)
	execSQL(t, f.conn, "INSERT INTO "+schema+".widgets (id, name, tenant_id, created_by, updated_by, private, binary_data, metadata) VALUES ($1, 'Portable Widget', $2, $3, $4, 'secret-field', decode('00ffabcd', 'hex'), '{\"large\":9007199254740993}')", widgetID, source.ID, oldCreated, oldMatched)

	fileIDs := []string{uuid.NewV7().String(), uuid.NewV7().String()}
	fileBodies := map[string][]byte{}
	for _, id := range fileIDs {
		content := []byte("contents for " + id)
		sum := sha256.Sum256(content)
		key := storage.BuildKey("attachments", source.ID, id, ".txt")
		if _, err := f.storage.Upload(ctx, key, bytes.NewReader(content), storage.UploadOptions{}); err != nil {
			t.Fatal(err)
		}
		if err := files.NewStore(f.conn).Insert(ctx, source.Slug, files.InsertRow{
			ID: id, TenantID: source.ID, StorageKey: key, OriginalName: "note.txt", ContentType: "text/plain",
			SizeBytes: int64(len(content)), ChecksumSHA256: hex.EncodeToString(sum[:]), UploadedBy: oldCreated, Purpose: "attachments",
		}); err != nil {
			t.Fatal(err)
		}
		fileBodies[id] = content
	}

	deletedID := uuid.NewV7().String()
	deletedKey := storage.BuildKey("attachments", source.ID, deletedID, ".txt")
	execSQL(t, f.conn, "INSERT INTO "+schema+".files (id, tenant_id, storage_key, original_name, content_type, size_bytes, checksum_sha256, purpose, deleted_at) VALUES ($1, $2, $3, 'deleted.txt', 'text/plain', 0, 'not-stored', 'attachments', NOW())", deletedID, source.ID, deletedKey)

	var database string
	if err := f.conn.QueryRowContext(ctx, "SELECT current_database()").Scan(&database); err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.New(ctx, "postgres://goerp:dev@localhost:15432/"+database)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := jobqueue.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })
	exportWorker := &tenantexport.Worker{TenantStore: f.worker.TenantStore, Registry: f.reg, RawDB: f.conn, Checkpoints: checkpoint.NewStore(f.conn), StorageBackend: f.storage, Keys: f.keys}
	tester := rivertest.NewWorker(t, riverpgxv5.New(pool), &river.Config{Schema: jobqueue.Schema}, exportWorker)
	exported, err := tester.Work(ctx, t, tx, tenantexport.Args{TenantID: source.ID, TenantSlug: source.Slug}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var output struct {
		Output tenantexport.Result `json:"output"`
	}
	if err := json.Unmarshal(exported.Job.Metadata, &output); err != nil {
		t.Fatal(err)
	}
	archiveKey := fmt.Sprintf("exports/%s/%d/archive.zip.enc", source.ID, exported.Job.ID)
	rc, _, err := f.storage.Download(ctx, archiveKey)
	if err != nil {
		t.Fatal(err)
	}
	encrypted, err := io.ReadAll(rc)
	_ = rc.Close()
	if err != nil {
		t.Fatal(err)
	}
	key, err := f.keys.Decrypt([]byte(output.Output.DecryptionKey))
	if err != nil {
		t.Fatal(err)
	}
	plain, err := decryptArchive(encrypted, string(key))
	if err != nil {
		t.Fatal(err)
	}
	man, archiveData, err := parseArchive(plain)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateArchive(man, archiveData); err != nil {
		t.Fatal(err)
	}
	for _, table := range transfer.Tables() {
		if _, ok := archiveData[table.File]; !ok {
			t.Errorf("missing table %s", table.Name)
		}
	}
	if _, ok := archiveData["files/"+deletedID]; ok {
		t.Error("deleted file object was exported")
	}
	for name, content := range archiveData {
		for _, secret := range []string{"secret-invite-hash", "secret-config", "secret-push-token", "secret-field"} {
			if bytes.Contains(content, []byte(secret)) {
				t.Errorf("secret %s in %s", secret, name)
			}
		}
	}

	// Removing source accounts simulates transferring into an environment with different IDs.
	execSQL(t, f.conn, "DELETE FROM system.users WHERE id IN ($1, $2)", oldMatched, oldCreated)
	matchedID, err := users.FindOrCreateInvited(ctx, "matched@transfer.test")
	if err != nil {
		t.Fatal(err)
	}
	if err := users.EnsureProfile(ctx, matchedID, "Target Name"); err != nil {
		t.Fatal(err)
	}
	if err := users.SetPassword(ctx, matchedID, "target-password-hash"); err != nil {
		t.Fatal(err)
	}

	corruptBody := archiveData["files/"+fileIDs[0]]
	archiveData["files/"+fileIDs[0]] = []byte("corrupted object")
	corruptCipher, corruptKey := encryptTestArchive(t, man, archiveData)
	archiveData["files/"+fileIDs[0]] = corruptBody
	if _, err := f.storage.Upload(ctx, "imports/corrupt.enc", bytes.NewReader(corruptCipher), storage.UploadOptions{}); err != nil {
		t.Fatal(err)
	}
	corruptJob := testJob(time.Now().UnixNano(), Args{NewSlug: "corrupttarget", InputRef: "imports/corrupt.enc", DecryptionKey: encryptedArgsKey(t, f.keys, corruptKey)})
	t.Cleanup(func() { _ = tenantschema.Drop(context.Background(), f.conn, "corrupttarget") })
	if _, err := f.worker.run(ctx, corruptJob); err == nil || !strings.Contains(err.Error(), "checksum mismatch for file") {
		t.Fatalf("corrupted object import: %v", err)
	}
	corruptTarget, err := f.worker.TenantStore.GetBySlug(ctx, "corrupttarget")
	if err != nil || corruptTarget.Status != "provisioning" {
		t.Fatalf("corrupt import activation: %+v, %v", corruptTarget, err)
	}
	corruptJob.Attempt = corruptJob.MaxAttempts
	if _, err := f.worker.run(ctx, corruptJob); err == nil {
		t.Fatal("corrupt import unexpectedly passed")
	}
	if _, err := f.worker.TenantStore.GetBySlug(ctx, "corrupttarget"); !errors.Is(err, tenant.ErrTenantNotFound) {
		t.Fatalf("final failure retained tenant: %v", err)
	}
	var retainedSchema bool
	if err := f.conn.QueryRowContext(ctx, "SELECT to_regnamespace('tenant_corrupttarget') IS NOT NULL").Scan(&retainedSchema); err != nil || retainedSchema {
		t.Fatalf("failed import retained schema: %t, %v", retainedSchema, err)
	}
	// Isolate the successful restore's account counts from the corrupt import's accounts.
	execSQL(t, f.conn, "DELETE FROM system.users WHERE email = 'created@transfer.test'")

	mailer := &importMailer{fail: true}
	invites := invite.NewStore(f.conn, users, role.NewStore(f.conn), nil, mailer)
	f.worker.Invites = invites
	backend := &interruptedStorage{Backend: f.storage, uploads: make(map[string]int)}
	f.worker.StorageBackend = backend
	job := testJob(time.Now().UnixNano(), Args{NewSlug: "transfertarget", InputRef: archiveKey, DecryptionKey: output.Output.DecryptionKey})
	target, err := f.worker.resolveTenant(ctx, job)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tenantschema.Drop(context.Background(), f.conn, target.Slug) })
	backend.failKey = strings.Replace(storage.BuildKey("attachments", source.ID, fileIDs[1], ".txt"), source.ID, target.ID, 1)
	if _, err := f.worker.run(ctx, job); err == nil || !strings.Contains(err.Error(), "interrupted upload") {
		t.Fatalf("first attempt: %v", err)
	}
	current, err := f.worker.TenantStore.GetBySlug(ctx, target.Slug)
	if err != nil || current.Status != "provisioning" || len(mailer.emails) != 0 {
		t.Fatalf("partial file import activated tenant or sent invitations: %+v, %v", current, err)
	}
	job.Attempt++
	if _, err := f.worker.run(ctx, job); err == nil || !strings.Contains(err.Error(), "mail unavailable") {
		t.Fatalf("mail attempt: %v", err)
	}
	job.Attempt++
	result, err := f.worker.run(ctx, job)
	if err != nil {
		t.Fatal(err)
	}
	if result.AccountsMatched != 1 || result.AccountsCreated != 1 {
		t.Fatalf("account counts: %+v", result)
	}
	if len(mailer.emails) != 1 || mailer.emails[0] != "created@transfer.test" {
		t.Fatalf("invitations: %v", mailer.emails)
	}
	again, err := f.worker.run(ctx, job)
	if err != nil || again.AccountsCreated != 1 || len(mailer.emails) != 1 {
		t.Fatalf("completed retry: %+v, %v", again, err)
	}

	var createdID, status, phone, title, contact, suspendedBy string
	targetSchema := tenantschema.Name(target.Slug)
	if err := f.conn.QueryRowContext(ctx, "SELECT m.user_id, m.status, m.phone, m.job_title, m.contact_id, m.suspended_by FROM "+targetSchema+".tenant_members m JOIN system.users u ON u.id = m.user_id WHERE u.email = 'created@transfer.test'").Scan(&createdID, &status, &phone, &title, &contact, &suspendedBy); err != nil {
		t.Fatal(err)
	}
	if createdID == oldCreated || status != "suspended" || phone != "+233201234567" || title != "Auditor" || contact != contactID || suspendedBy != matchedID {
		t.Fatalf("restored member: %s, %s, %s, %s, %s, %s", createdID, status, phone, title, contact, suspendedBy)
	}
	var creator, updater, tenantID string
	if err := f.conn.QueryRowContext(ctx, "SELECT created_by, updated_by, tenant_id FROM "+targetSchema+".widgets WHERE id = $1", widgetID).Scan(&creator, &updater, &tenantID); err != nil {
		t.Fatal(err)
	}
	if creator != createdID || updater != matchedID || tenantID != target.ID {
		t.Fatalf("module identity mapping: %s, %s, %s", creator, updater, tenantID)
	}
	var binaryData []byte
	var large int64
	if err := f.conn.QueryRowContext(ctx, "SELECT binary_data, (metadata->>'large')::bigint FROM "+targetSchema+".widgets WHERE id = $1", widgetID).Scan(&binaryData, &large); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(binaryData, []byte{0, 255, 171, 205}) || large != 9007199254740993 {
		t.Fatalf("module data changed: %x, %d", binaryData, large)
	}

	var memberCount int
	if err := f.conn.QueryRowContext(ctx, "SELECT count(*) FROM system.tenant_memberships WHERE tenant_id = $1", target.ID).Scan(&memberCount); err != nil || memberCount != 2 {
		t.Fatalf("membership index count %d, %v", memberCount, err)
	}
	for _, id := range fileIDs {
		row, err := files.NewStore(f.conn).GetByID(ctx, target.Slug, id)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(row.StorageKey, "attachments/"+target.ID+"/") {
			t.Errorf("file key %s", row.StorageKey)
		}
		object, _, err := backend.Download(ctx, row.StorageKey)
		if err != nil {
			t.Fatal(err)
		}
		content, err := io.ReadAll(object)
		_ = object.Close()
		if err != nil || !bytes.Equal(content, fileBodies[id]) {
			t.Errorf("restored file %s: %q, %v", id, content, err)
		}
		expected := 1
		if id == fileIDs[1] {
			expected = 2
		}
		if backend.uploads[row.StorageKey] != expected {
			t.Errorf("file %s uploaded %d times, want %d", id, backend.uploads[row.StorageKey], expected)
		}
	}

	invitation, err := invites.GetLiveByToken(ctx, target.Slug, mailer.token)
	if err != nil {
		t.Fatal(err)
	}
	if err := invites.Accept(ctx, target.Slug, invitation.ID, createdID, func(tx *sql.Tx) error { return users.ActivateWithPasswordTx(ctx, tx, createdID, "new-password-hash") }); err != nil {
		t.Fatal(err)
	}
	var acceptedStatus, grantedBy string
	var restoredGrantedAt, restoredExpiresAt time.Time
	if err := f.conn.QueryRowContext(ctx, "SELECT m.status, ur.granted_by, ur.granted_at, ur.expires_at FROM "+targetSchema+".tenant_members m JOIN "+targetSchema+".user_roles ur ON ur.user_id = m.user_id WHERE m.user_id = $1 AND ur.role_id = $2", createdID, roleID).Scan(&acceptedStatus, &grantedBy, &restoredGrantedAt, &restoredExpiresAt); err != nil {
		t.Fatal(err)
	}
	if acceptedStatus != "suspended" || grantedBy != matchedID || !restoredGrantedAt.Equal(grantedAt) || !restoredExpiresAt.Equal(expiresAt) {
		t.Fatalf("acceptance changed restored membership/grant: %s, %s, %s, %s", acceptedStatus, grantedBy, restoredGrantedAt, restoredExpiresAt)
	}
}
