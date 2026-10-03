package adminapi

import (
	"context"
	"database/sql"
	"encoding/json/v2"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
	"uuid"

	"github.com/djangbahevans/goerp/internal/engine/auditlog"
	"github.com/djangbahevans/goerp/internal/engine/auth/session"
	"github.com/djangbahevans/goerp/internal/engine/auth/sessionrevoke"
	"github.com/djangbahevans/goerp/internal/engine/authaudit"
	"github.com/djangbahevans/goerp/internal/engine/authaudit/audittest"
	"github.com/djangbahevans/goerp/internal/engine/cache"
	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/internal/engine/jobqueue"
	"github.com/djangbahevans/goerp/internal/engine/mfa"
	"github.com/djangbahevans/goerp/internal/engine/role"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
	"github.com/djangbahevans/goerp/internal/engine/user"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverdatabasesql"
	"github.com/riverqueue/river/rivermigrate"
	"github.com/riverqueue/river/rivertype"
	"github.com/vmihailenco/msgpack/v5"
)

type accountMail struct{ emails []string }

func (m *accountMail) SendOperatorMFAReset(_ context.Context, email string) error {
	m.emails = append(m.emails, email)
	return nil
}

type accountFixture struct {
	db                                *sql.DB
	deps                              AccountDeps
	server                            *Server
	userID, otherID, email, jobSchema string
	tenants                           []*tenant.Tenant
	sessions                          []string
	revoker                           *sessionrevoke.Revoker
	mail                              *accountMail
}

func newAccountFixture(t *testing.T) *accountFixture {
	t.Helper()
	ctx := t.Context()
	conn, err := db.New(localPostgresDSN)
	if err != nil {
		t.Skipf("dev Postgres unavailable: %v", err)
	}

	adminDB := conn
	t.Cleanup(func() { _ = adminDB.Close() })

	// A private database keeps schema-sensitive account tests independent of shared dev data.
	databaseName := "operatoraccounts" + strings.ReplaceAll(uuid.NewV7().String(), "-", "")
	if _, err := conn.ExecContext(ctx, "CREATE DATABASE "+databaseName); err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		if _, err := adminDB.ExecContext(context.Background(), "DROP DATABASE "+databaseName); err != nil {
			t.Errorf("drop private account test database: %v", err)
		}
	})

	dsn, err := url.Parse(localPostgresDSN)
	if err != nil {
		t.Fatal(err)
	}

	dsn.Path = "/" + databaseName
	conn, err = db.New(dsn.String())
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = conn.Close() })
	if _, err := conn.ExecContext(ctx, `CREATE SCHEMA partman; CREATE EXTENSION pg_partman SCHEMA partman`); err != nil {
		t.Fatal(err)
	}

	client, err := cache.New(ctx, cache.Config{Addr: "localhost:6379", MaxRetries: 1})
	if err != nil {
		t.Skipf("dev Redis unavailable: %v", err)
	}

	t.Cleanup(func() { _ = client.Close() })
	tenants := tenant.NewStore(conn)
	users := user.NewStore(conn)
	sessions := session.NewStore(conn)
	roles := role.NewStore(conn)
	authAudit := authaudit.NewStore(conn, tenants)
	adminAudit := auditlog.NewStore(conn)
	for _, bootstrap := range []func(context.Context) error{
		tenants.Bootstrap,
		users.Bootstrap,
		roles.BootstrapMembershipIndex,
		sessions.Bootstrap,
		mfa.NewStore(conn).Bootstrap,
		authAudit.Bootstrap,
		adminAudit.Bootstrap,
	} {
		if err := bootstrap(ctx); err != nil {
			t.Fatal(err)
		}
	}

	f := &accountFixture{
		db:        conn,
		mail:      &accountMail{},
		email:     uuid.NewV7().String() + "@operator.test",
		jobSchema: fmt.Sprintf("accountjobs%d", time.Now().UnixNano()),
	}
	f.userID, err = users.CreateRegistered(ctx, f.email, "hash", user.StatusActive)
	if err != nil {
		t.Fatal(err)
	}

	f.otherID, err = users.CreateRegistered(ctx, uuid.NewV7().String()+"@operator.test", "hash", user.StatusActive)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		_, _ = conn.Exec(`DELETE FROM system.auth_audit_log WHERE user_id IN ($1, $2)`, f.userID, f.otherID)
		_, _ = conn.Exec(`DELETE FROM system.user_mfa WHERE user_id IN ($1, $2)`, f.userID, f.otherID)
		_, _ = conn.Exec(`DELETE FROM system.admin_audit_log WHERE target_scope IN ($1, $2)`, f.userID, f.otherID)
		_, _ = conn.Exec(`DELETE FROM system.users WHERE id IN ($1, $2)`, f.userID, f.otherID)
	})

	for i := range 2 {
		tt, err := tenants.CreateTenant(ctx, fmt.Sprintf("operator%d%d", i, time.Now().UnixNano()), "Operator Test")
		if err != nil {
			t.Fatal(err)
		}

		f.tenants = append(f.tenants, tt)
		schema := tenantschema.Name(tt.Slug)
		if _, err := conn.Exec("CREATE SCHEMA " + schema); err != nil {
			t.Fatal(err)
		}

		t.Cleanup(func() {
			_, _ = conn.Exec("DROP SCHEMA " + schema + " CASCADE")
			_, _ = conn.Exec(`DELETE FROM system.tenants WHERE id = $1`, tt.ID)
		})

		if err := roles.Bootstrap(ctx, tt.Slug); err != nil {
			t.Fatal(err)
		}

		if err := roles.AttachMembershipTrigger(ctx, tt.Slug); err != nil {
			t.Fatal(err)
		}

		for _, id := range []string{f.userID, f.otherID} {
			if err := role.AddMemberTx(ctx, conn, tt.Slug, id); err != nil {
				t.Fatal(err)
			}

			if _, err := conn.Exec("UPDATE "+schema+".tenant_members SET mfa_reset_at = NOW() WHERE user_id = $1", id); err != nil {
				t.Fatal(err)
			}

			sid := uuid.NewV7().String()
			if err := sessions.Insert(ctx, session.Row{ID: sid, UserID: id, TenantID: tt.ID, DeviceID: uuid.NewV7().String(), RefreshHash: sid, ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
				t.Fatal(err)
			}

			if id == f.userID {
				f.sessions = append(f.sessions, sid)
			}
		}
	}

	if _, err := conn.Exec("UPDATE "+tenantschema.Name(f.tenants[1].Slug)+".tenant_members SET status = 'suspended' WHERE user_id = $1", f.userID); err != nil {
		t.Fatal(err)
	}

	f.revoker = sessionrevoke.NewRevoker(sessions, client)
	if _, err := conn.Exec("CREATE SCHEMA " + f.jobSchema); err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _, _ = conn.Exec("DROP SCHEMA " + f.jobSchema + " CASCADE") })
	driver := riverdatabasesql.New(conn)
	migrator, err := rivermigrate.New(driver, &rivermigrate.Config{Schema: f.jobSchema})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := migrator.Migrate(ctx, rivermigrate.DirectionUp, nil); err != nil {
		t.Fatal(err)
	}

	jobs, err := river.NewClient(driver, &river.Config{Schema: f.jobSchema})
	if err != nil {
		t.Fatal(err)
	}

	f.deps = AccountDeps{
		DB:       conn,
		Audit:    authAudit,
		Sessions: f.revoker,
		Jobs:     jobs,
		Mailer:   f.mail,
	}
	f.register(t, adminAudit)
	return f
}

func (f *accountFixture) register(t *testing.T, audit *auditlog.Store) {
	t.Helper()
	var err error
	f.server, err = NewServer(&Config{
		AdminToken:    "operator-token",
		AuditStore:    audit,
		MaxBodyBytes:  64 * 1024,
		MaxConcurrent: 10,
	})
	if err != nil {
		t.Fatal(err)
	}

	RegisterAccountRoutes(f.server.Router(), f.deps)
}

func (f *accountFixture) request(t *testing.T, method, suffix, body, token string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, "/admin/accounts/"+f.userID+suffix, strings.NewReader(body)).WithContext(t.Context())
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set(operatorIdentityHeader, "operator@example.test")
	w := httptest.NewRecorder()
	f.server.http.Handler.ServeHTTP(w, r)
	return w
}

func (f *accountFixture) count(t *testing.T, query string, args ...any) int {
	t.Helper()
	var n int
	if err := f.db.QueryRowContext(t.Context(), query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}

	return n
}

func (f *accountFixture) assertRevoked(t *testing.T) {
	t.Helper()
	if n := f.count(t, `SELECT count(*) FROM system.sessions WHERE user_id = $1 AND revoked_at IS NULL`, f.userID); n != 0 {
		t.Fatalf("unrevoked target sessions = %d", n)
	}

	if n := f.count(t, `SELECT count(*) FROM system.sessions WHERE user_id = $1 AND revoked_at IS NULL`, f.otherID); n != 2 {
		t.Fatalf("other account sessions = %d, want 2", n)
	}

	for _, id := range f.sessions {
		blocked, err := f.revoker.IsBlocked(t.Context(), id)
		if err != nil || !blocked {
			t.Errorf("session %s blocked=%t, err=%v", id, blocked, err)
		}
	}
}

func TestAccountLifecycle(t *testing.T) {
	f := newAccountFixture(t)
	for _, token := range []string{"", "tenant-access-token"} {
		if w := f.request(t, http.MethodPost, "/suspend", `{"reason":"compromised"}`, token); w.Code != 401 {
			t.Fatalf("tenant token status = %d", w.Code)
		}
	}

	for _, body := range []string{`{}`, `{"reason":" "}`, `{"REASON":"test"}`, `{"reason":"one","reason":"two"}`, `{`} {
		if w := f.request(t, http.MethodPost, "/suspend", body, "operator-token"); w.Code != 400 {
			t.Fatalf("body %s: %d", body, w.Code)
		}
	}

	w := f.request(t, http.MethodPost, "/suspend", `{"reason":"compromised"}`, "operator-token")
	if w.Code != 204 || w.Body.Len() != 0 {
		t.Fatalf("suspend = %d %s", w.Code, w.Body)
	}

	f.assertRevoked(t)
	if n := f.count(t, `SELECT count(*) FROM system.users WHERE id = $1 AND status = 'suspended'`, f.userID); n != 1 {
		t.Fatal("account not suspended")
	}

	audittest.AssertLatest(t, f.db, "", "account.suspended", f.userID, "")
	if n := f.count(t, `SELECT count(*) FROM system.admin_audit_log WHERE target_scope = $1 AND operator_identity = 'operator@example.test' AND reason = 'compromised' AND status_code = 204`, f.userID); n != 1 {
		t.Fatalf("operator audit rows = %d", n)
	}

	f.assertEvents(t, "system.user.suspended")
	if w := f.request(t, http.MethodPost, "/suspend", `{"reason":"again"}`, "operator-token"); w.Code != 409 || decodeEnvelope(t, w).Error.Code != "user_not_active" {
		t.Fatal("repeated suspension must conflict")
	}

	if w := f.request(t, http.MethodPost, "/unsuspend", "", "operator-token"); w.Code != 204 {
		t.Fatalf("unsuspend = %d %s", w.Code, w.Body)
	}

	audittest.AssertLatest(t, f.db, "", "account.unsuspended", f.userID, "")
	if w := f.request(t, http.MethodPost, "/unsuspend", "", "operator-token"); w.Code != 409 {
		t.Fatal("unsuspend active account must conflict")
	}

	if w := f.request(t, http.MethodDelete, "", "", "operator-token"); w.Code != 204 {
		t.Fatalf("delete = %d %s", w.Code, w.Body)
	}

	if n := f.count(t, `SELECT count(*) FROM system.users WHERE id=$1 AND status='deleted' AND deleted_at IS NOT NULL`, f.userID); n != 1 {
		t.Fatal("account not soft deleted")
	}

	audittest.AssertLatest(t, f.db, "", "account.deleted", f.userID, "")
	f.assertEvents(t, "system.user.deleted")
	if w := f.request(t, http.MethodPost, "/unsuspend", "", "operator-token"); w.Code != 409 {
		t.Fatal("deleted account must not be reactivated")
	}

	if w := f.request(t, http.MethodDelete, "", "", "operator-token"); w.Code != 204 {
		t.Fatal("delete is not idempotent")
	}

	if n := f.count(t, `SELECT count(*) FROM system.auth_audit_log WHERE user_id=$1 AND event_type='account.deleted'`, f.userID); n != 1 {
		t.Fatalf("duplicate delete events = %d", n)
	}
}

func (f *accountFixture) assertEvents(t *testing.T, name string) {
	t.Helper()
	rows, err := f.db.Query(`SELECT args FROM `+f.jobSchema+`.river_job WHERE kind='event_delivery' AND args->>'event_name'=$1`, name)
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = rows.Close() }()
	var tenantIDs []string
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			t.Fatal(err)
		}

		var event jobqueue.EventDeliveryArgs
		if err := json.Unmarshal(raw, &event); err != nil {
			t.Fatal(err)
		}

		var payload map[string]any
		if err := msgpack.Unmarshal(event.Payload, &payload); err != nil {
			t.Fatal(err)
		}

		if event.EmitterModule != "system" || event.UserID != "" || !event.Transactional || event.EventVersion != 1 || event.EmittedAt.IsZero() || payload["user_id"] != f.userID {
			t.Errorf("unexpected event: %+v payload=%v", event, payload)
		}

		if name == "system.user.deleted" && payload["email"] != f.email {
			t.Errorf("delete payload = %v", payload)
		}

		if name == "system.user.suspended" && (payload["reason"] != "compromised" || payload["scope"] != "account" || payload["suspended_by"] != nil) {
			t.Errorf("suspend payload = %v", payload)
		}

		tenantIDs = append(tenantIDs, event.TenantID)
	}

	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}

	if len(tenantIDs) != 2 || !slices.Contains(tenantIDs, f.tenants[0].ID) || !slices.Contains(tenantIDs, f.tenants[1].ID) {
		t.Errorf("event tenants = %v", tenantIDs)
	}
}

func TestAccountMFAResetAndDryRuns(t *testing.T) {
	f := newAccountFixture(t)
	ctx := t.Context()
	factors := mfa.NewStore(f.db)
	for _, id := range []string{f.userID, f.otherID} {
		for _, tenantID := range []*string{nil, new(f.tenants[0].ID), new(f.tenants[1].ID)} {
			for _, kind := range []mfa.CredentialType{mfa.CredentialTOTP, mfa.CredentialRecoveryCode} {
				if err := factors.WithTx(ctx, func(tx *sql.Tx) error {
					_, err := factors.InsertScopedTx(ctx, tx, id, tenantID, kind, []byte("secret"), nil)
					return err
				}); err != nil {
					t.Fatal(err)
				}
			}
		}
	}

	expiredID := uuid.NewV7().String()
	if err := session.NewStore(f.db).Insert(ctx, session.Row{ID: expiredID, UserID: f.userID, TenantID: f.tenants[0].ID, DeviceID: uuid.NewV7().String(), RefreshHash: expiredID, ExpiresAt: time.Now().Add(-time.Hour)}); err != nil {
		t.Fatal(err)
	}

	for _, suffix := range []string{"?dry_run=true", "/mfa/reset?dry_run=true"} {
		method := http.MethodDelete
		if strings.HasPrefix(suffix, "/mfa") {
			method = http.MethodPost
		}

		w := f.request(t, method, suffix, "", "operator-token")
		if w.Code != 200 {
			t.Fatalf("dry run = %d %s", w.Code, w.Body)
		}

		var env struct {
			Data accountPreview `json:"data"`
		}

		if err := json.UnmarshalRead(w.Body, &env); err != nil {
			t.Fatal(err)
		}

		if env.Data.Email != f.email || env.Data.LiveSessions != 2 || !slices.Equal(env.Data.Tenants, []string{f.tenants[0].Slug, f.tenants[1].Slug}) {
			t.Fatalf("preview = %+v", env.Data)
		}
	}

	if n := f.count(t, `SELECT count(*) FROM system.user_mfa WHERE user_id=$1 AND revoked_at IS NULL`, f.userID); n != 6 {
		t.Fatalf("dry run factors = %d", n)
	}

	if n := f.count(t, `SELECT count(*) FROM system.sessions WHERE user_id=$1 AND revoked_at IS NULL`, f.userID); n != 3 {
		t.Fatal("dry run revoked sessions")
	}

	if n := f.count(t, `SELECT count(*) FROM system.auth_audit_log WHERE user_id=$1`, f.userID); n != 0 {
		t.Fatal("dry run wrote auth event")
	}

	if len(f.mail.emails) != 0 {
		t.Fatal("dry run sent email")
	}

	for _, suffix := range []string{
		"?dry_run=invalid",
		"?dry_run=",
		"?dry_run=true&dry_run=false",
		"?dry_run=true;other=value",
		"?dry_run=%zz",
	} {
		if w := f.request(t, http.MethodDelete, suffix, "", "operator-token"); w.Code != 400 {
			t.Fatal("invalid dry_run accepted")
		}
	}

	w := f.request(t, http.MethodPost, "/mfa/reset", "", "operator-token")
	if w.Code != 204 {
		t.Fatalf("reset = %d %s", w.Code, w.Body)
	}

	f.assertRevoked(t)
	audittest.AssertLatest(t, f.db, "", "mfa.operator_reset", f.userID, "")
	if n := f.count(t, `SELECT count(*) FROM system.user_mfa WHERE user_id=$1 AND revoked_at IS NULL`, f.userID); n != 0 {
		t.Fatalf("active factors = %d", n)
	}

	if n := f.count(t, `SELECT count(*) FROM system.user_mfa WHERE user_id=$1 AND revoked_at IS NULL`, f.otherID); n != 6 {
		t.Fatalf("other user's factors = %d", n)
	}

	for _, tt := range f.tenants {
		if n := f.count(t, "SELECT count(*) FROM "+tenantschema.Name(tt.Slug)+".tenant_members WHERE user_id=$1 AND mfa_reset_at IS NOT NULL", f.userID); n != 0 {
			t.Fatal("target reset barrier retained")
		}

		if n := f.count(t, "SELECT count(*) FROM "+tenantschema.Name(tt.Slug)+".tenant_members WHERE user_id=$1 AND mfa_reset_at IS NOT NULL", f.otherID); n != 1 {
			t.Fatal("other reset barrier cleared")
		}
	}

	if !slices.Equal(f.mail.emails, []string{f.email}) {
		t.Fatalf("email recipients = %v", f.mail.emails)
	}
}

type failingAccountAudit struct{}

func (failingAccountAudit) InsertTx(context.Context, *sql.Tx, authaudit.Row) error {
	return errors.New("audit unavailable")
}

func TestAccountChangesRollbackWithoutAudit(t *testing.T) {
	for _, action := range []string{"suspend", "delete", "mfa/reset"} {
		t.Run(action, func(t *testing.T) {
			f := newAccountFixture(t)
			if _, err := mfa.NewStore(f.db).Insert(t.Context(), f.userID, mfa.CredentialTOTP, []byte("secret"), nil); err != nil {
				t.Fatal(err)
			}

			f.deps.Audit = failingAccountAudit{}
			f.register(t, auditlog.NewStore(f.db))
			method, suffix, body := http.MethodPost, "/"+action, ""
			if action == "delete" {
				method, suffix = http.MethodDelete, ""
			}

			if action == "suspend" {
				body = `{"reason":"compromised"}`
			}

			if w := f.request(t, method, suffix, body, "operator-token"); w.Code != 500 {
				t.Fatalf("failed audit status = %d", w.Code)
			}

			if n := f.count(t, `SELECT count(*) FROM system.users WHERE id=$1 AND status='active' AND deleted_at IS NULL`, f.userID); n != 1 {
				t.Fatal("account change not rolled back")
			}

			if n := f.count(t, `SELECT count(*) FROM system.sessions WHERE user_id=$1 AND revoked_at IS NULL`, f.userID); n != 2 {
				t.Fatal("session revocation not rolled back")
			}

			if n := f.count(t, `SELECT count(*) FROM system.user_mfa WHERE user_id=$1 AND revoked_at IS NULL`, f.userID); n != 1 {
				t.Fatal("factor revocation not rolled back")
			}

			if len(f.mail.emails) != 0 {
				t.Fatal("failed transaction sent email")
			}
		})
	}
}

func TestAccountDeleteRacesUnsuspend(t *testing.T) {
	f := newAccountFixture(t)
	if _, err := f.db.Exec(`UPDATE system.users SET status='suspended' WHERE id=$1`, f.userID); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	wg.Go(func() { f.request(t, http.MethodDelete, "", "", "operator-token") })
	wg.Go(func() { f.request(t, http.MethodPost, "/unsuspend", "", "operator-token") })
	wg.Wait()
	if n := f.count(t, `SELECT count(*) FROM system.users WHERE id=$1 AND status='deleted' AND deleted_at IS NOT NULL`, f.userID); n != 1 {
		t.Fatal("race reactivated deleted account")
	}
}

type accountCacheFailure struct {
	AccountSessions
}

func (accountCacheFailure) Blocklist(context.Context, []string) error {
	return errors.New("Redis unavailable")
}

func TestAccountMFAResetRetryBlocksPreviouslyRevokedSessions(t *testing.T) {
	f := newAccountFixture(t)
	f.deps.Sessions = accountCacheFailure{AccountSessions: f.revoker}
	f.register(t, auditlog.NewStore(f.db))

	w := f.request(t, http.MethodPost, "/mfa/reset", "", "operator-token")
	if w.Code != 500 || !strings.Contains(decodeEnvelope(t, w).Error.Message, "committed") {
		t.Fatalf("blocklist failure = %d %s", w.Code, w.Body)
	}

	if n := f.count(t, `SELECT count(*) FROM system.sessions WHERE user_id=$1 AND revoked_at IS NULL`, f.userID); n != 0 {
		t.Fatal("session revocation did not commit")
	}

	f.deps.Sessions = f.revoker
	f.register(t, auditlog.NewStore(f.db))
	w = f.request(t, http.MethodPost, "/mfa/reset", "", "operator-token")
	if w.Code != 204 {
		t.Fatalf("reset retry = %d %s", w.Code, w.Body)
	}

	f.assertRevoked(t)
}

type accountEventFailure struct {
	AccountJobs
	calls int
}

func (j *accountEventFailure) InsertTx(ctx context.Context, tx *sql.Tx, args river.JobArgs, opts *river.InsertOpts) (*rivertype.JobInsertResult, error) {
	j.calls++
	if j.calls == 2 {
		return nil, errors.New("event queue unavailable")
	}

	return j.AccountJobs.InsertTx(ctx, tx, args, opts)
}

func TestAccountEventFailureRollsBackEveryTenant(t *testing.T) {
	f := newAccountFixture(t)
	f.deps.Jobs = &accountEventFailure{AccountJobs: f.deps.Jobs}
	f.register(t, auditlog.NewStore(f.db))

	if w := f.request(t, http.MethodDelete, "", "", "operator-token"); w.Code != 500 {
		t.Fatalf("event failure status = %d", w.Code)
	}

	if n := f.count(t, `SELECT count(*) FROM system.users WHERE id=$1 AND status='active' AND deleted_at IS NULL`, f.userID); n != 1 {
		t.Fatal("account change did not roll back")
	}

	if n := f.count(t, `SELECT count(*) FROM system.sessions WHERE user_id=$1 AND revoked_at IS NULL`, f.userID); n != 2 {
		t.Fatal("session revocation did not roll back")
	}

	if n := f.count(t, `SELECT count(*) FROM system.auth_audit_log WHERE user_id=$1`, f.userID); n != 0 {
		t.Fatal("auth event did not roll back")
	}

	if n := f.count(t, "SELECT count(*) FROM "+f.jobSchema+".river_job"); n != 0 {
		t.Fatal("first tenant's event did not roll back")
	}
}

func TestAccountInvalidTargetsAndStatuses(t *testing.T) {
	f := newAccountFixture(t)
	for _, tc := range []struct {
		id     string
		status int
	}{
		{"invalid", http.StatusBadRequest},
		{uuid.NewV7().String(), http.StatusNotFound},
	} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodDelete, "/admin/accounts/"+tc.id, nil)
		r.Header.Set("Authorization", "Bearer operator-token")
		f.server.http.Handler.ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Errorf("target %s status = %d, want %d", tc.id, w.Code, tc.status)
		}
	}

	if _, err := f.db.Exec(`UPDATE system.users SET status='pending_verification' WHERE id=$1`, f.userID); err != nil {
		t.Fatal(err)
	}

	if w := f.request(t, http.MethodPost, "/suspend", `{"reason":"compromised"}`, "operator-token"); w.Code != 409 {
		t.Fatalf("pending account suspend = %d", w.Code)
	}

	if n := f.count(t, `SELECT count(*) FROM system.sessions WHERE user_id=$1 AND revoked_at IS NULL`, f.userID); n != 2 {
		t.Fatal("invalid transition revoked sessions")
	}
}

func TestAccountUnsuspendPreservesSessionRevocationAfterCacheFailure(t *testing.T) {
	f := newAccountFixture(t)
	f.deps.Sessions = accountCacheFailure{AccountSessions: f.revoker}
	f.register(t, auditlog.NewStore(f.db))

	if w := f.request(t, http.MethodPost, "/suspend", `{"reason":"compromised"}`, "operator-token"); w.Code != 500 {
		t.Fatalf("suspend with cache failure = %d", w.Code)
	}

	if w := f.request(t, http.MethodPost, "/unsuspend", "", "operator-token"); w.Code != 500 {
		t.Fatalf("unsuspend with cache failure = %d", w.Code)
	}

	if n := f.count(t, `SELECT count(*) FROM system.users WHERE id=$1 AND status='suspended'`, f.userID); n != 1 {
		t.Fatal("cache failure reactivated the account")
	}

	f.deps.Sessions = f.revoker
	f.register(t, auditlog.NewStore(f.db))
	if w := f.request(t, http.MethodPost, "/unsuspend", "", "operator-token"); w.Code != 204 {
		t.Fatalf("unsuspend retry = %d %s", w.Code, w.Body)
	}

	f.assertRevoked(t)
}
