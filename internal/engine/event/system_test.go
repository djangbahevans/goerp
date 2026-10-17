package event_test

import (
	"context"
	"database/sql"
	"encoding/json/v2"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
	"uuid"

	"github.com/djangbahevans/goerp/internal/engine/auth/membership/membershiptest"
	"github.com/djangbahevans/goerp/internal/engine/auth/session"
	"github.com/djangbahevans/goerp/internal/engine/event"
	"github.com/djangbahevans/goerp/internal/engine/eventdelivery"
	"github.com/djangbahevans/goerp/internal/engine/invite"
	"github.com/djangbahevans/goerp/internal/engine/jobqueue"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/registry"
	"github.com/djangbahevans/goerp/internal/engine/role"
	"github.com/djangbahevans/goerp/internal/engine/schema"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
	"github.com/djangbahevans/goerp/internal/engine/user"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverdatabasesql"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"
	"github.com/riverqueue/river/rivertest"
	"github.com/riverqueue/river/rivertype"
	"github.com/vmihailenco/msgpack/v5"
)

func TestSystemEvents_TriggersAndAsyncFanout(t *testing.T) {
	ctx := t.Context()
	conn := membershiptest.New(t)
	driver := riverdatabasesql.New(conn)
	migrator, err := rivermigrate.New(driver, &rivermigrate.Config{Schema: jobqueue.Schema})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := migrator.Migrate(ctx, rivermigrate.DirectionUp, nil); err != nil {
		t.Fatal(err)
	}
	jobs, err := river.NewClient(driver, &river.Config{Schema: jobqueue.Schema})
	if err != nil {
		t.Fatal(err)
	}
	emitter := event.NewSystemEmitter(conn, jobs)

	tenants := tenant.NewStore(conn)
	tenants.SystemEvents = emitter
	users := user.NewStore(conn)
	users.SystemEvents = emitter
	roles := role.NewStore(conn)
	roles.SystemEvents = emitter
	sessions := session.NewStore(conn)
	sessions.SystemEvents = emitter
	versions := schema.NewPool(conn, time.Second)
	versions.SystemEvents = emitter

	if err := sessions.Bootstrap(ctx); err != nil {
		t.Fatal(err)
	}
	if err := versions.Bootstrap(ctx); err != nil {
		t.Fatal(err)
	}
	tt, err := tenants.CreateTenant(ctx, "systemevents", "System Events")
	if err != nil {
		t.Fatal(err)
	}
	if err := tenantschema.Create(ctx, conn, tt.Slug); err != nil {
		t.Fatal(err)
	}
	if err := roles.Bootstrap(ctx, tt.Slug); err != nil {
		t.Fatal(err)
	}
	if err := roles.AttachMembershipTrigger(ctx, tt.Slug); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(ctx, `CREATE TABLE `+tenantschema.Name(tt.Slug)+`.event_log (
 id uuid NOT NULL, event_name text, event_version int, emitter_module text, payload bytea,
 trace_id text, user_id uuid, emitted_at timestamptz NOT NULL, PRIMARY KEY(id, emitted_at))`); err != nil {
		t.Fatal(err)
	}
	userID, err := users.CreateRegistered(ctx, "system@example.test", "hash", user.StatusActive)
	if err != nil {
		t.Fatal(err)
	}
	if err := users.EnsureProfile(ctx, userID, "Initial"); err != nil {
		t.Fatal(err)
	}
	if err := roles.AddMember(ctx, tt.Slug, userID); err != nil {
		t.Fatal(err)
	}
	if err := roles.AddMember(ctx, tt.Slug, userID); err != nil {
		t.Fatal(err)
	}
	if _, err := users.UpdateProfile(ctx, userID, user.ProfileUpdate{Name: new("Updated"), Locale: user.NullableField{Set: true, Value: new("en")}}); err != nil {
		t.Fatal(err)
	}
	if _, err := users.UpdateProfile(ctx, userID, user.ProfileUpdate{Name: new("Updated"), Locale: user.NullableField{Set: true, Value: new("en")}}); err != nil {
		t.Fatal(err)
	}
	if err := sessions.Insert(ctx, session.Row{
		ID:          uuid.NewV7().String(),
		UserID:      userID,
		TenantID:    tt.ID,
		DeviceID:    uuid.NewV7().String(),
		RefreshHash: "hash",
		IPAddress:   "127.0.0.1",
		ExpiresAt:   time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	if err := users.SetPasswordResetToken(ctx, userID, "reset-hash", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := users.ConsumePasswordResetToken(ctx, "reset-hash", "new-hash"); err != nil {
		t.Fatal(err)
	}
	if _, err := tenants.UpdateStatus(ctx, tt.Slug, tenant.StatusActive, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := tenants.UpdateStatus(ctx, tt.Slug, tenant.StatusActive, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := tenants.UpdateStatus(ctx, tt.Slug, tenant.StatusSuspended, new("billing")); err != nil {
		t.Fatal(err)
	}
	if _, err := tenants.UpdateStatus(ctx, tt.Slug, tenant.StatusActive, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := tenants.UpdatePlan(ctx, tt.Slug, tenant.PlanPro); err != nil {
		t.Fatal(err)
	}
	if _, err := tenants.UpdatePlan(ctx, tt.Slug, tenant.PlanPro); err != nil {
		t.Fatal(err)
	}
	deletion := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
	if _, err := tenants.BeginOffboardingScheduled(ctx, tt.Slug, deletion); err != nil {
		t.Fatal(err)
	}
	for _, version := range []string{"1.0.0", "1.1.0", "1.1.0"} {
		sess, err := versions.BeginSync(ctx, tt.ID, tt.Slug, "sales", &manifest.Manifest{Version: version})
		if err != nil {
			t.Fatal(err)
		}
		err = sess.RecordSyncSuccess(ctx)
		closeErr := sess.Close(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if closeErr != nil {
			t.Fatal(closeErr)
		}
	}
	expected := map[string]map[string]any{
		"system.user.created":               {"user_id": userID, "tenant_id": tt.ID, "email": "system@example.test", "name": "Initial", "locale": nil},
		"system.user.updated":               {"user_id": userID, "changed_fields": []any{"name", "locale"}, "name": "Updated", "locale": "en"},
		"system.user.logged_in":             {"user_id": userID, "tenant_id": tt.ID, "auth_method": "password", "ip": "127.0.0.1"},
		"system.user.password_reset":        {"user_id": userID, "tenant_id": tt.ID},
		"system.tenant.provisioned":         {"tenant_id": tt.ID, "slug": tt.Slug, "plan": "starter"},
		"system.tenant.suspended":           {"tenant_id": tt.ID, "reason": "billing", "suspended_by": nil},
		"system.tenant.unsuspended":         {"tenant_id": tt.ID},
		"system.tenant.plan_changed":        {"tenant_id": tt.ID, "old_plan": "starter", "new_plan": "pro"},
		"system.tenant.offboarding_started": {"tenant_id": tt.ID, "scheduled_deletion": deletion.Format(time.RFC3339Nano)},
		"system.module.installed":           {"module_name": "sales", "version": "1.0.0"},
		"system.module.upgraded":            {"module_name": "sales", "old_version": "1.0.0", "new_version": "1.1.0"},
	}
	reg := &registry.ModuleRegistry{SystemEvents: emitter}
	subs := make([]manifest.EventSubscription, 0, len(expected)+1)
	for name := range expected {
		subs = append(subs, manifest.EventSubscription{Name: name, Async: true, Handler: "handle_event"})
	}
	subs = append(subs, manifest.EventSubscription{Name: "system.module.uninstalled", Async: true, Handler: "handle_event"})
	if _, err := reg.Update(map[string]*module.LoadedModule{
		"subscriber": {Status: module.StatusReady, Manifest: manifest.Manifest{Name: "subscriber", Type: "standard", Version: "1.0.0", Subscribes: subs}},
		"sales":      {Status: module.StatusReady, Manifest: manifest.Manifest{Name: "sales", Type: "standard", Version: "1.1.0"}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.UninstallModule(ctx, nil, "sales", nil, nil); err != nil {
		t.Fatal(err)
	}
	expected["system.module.uninstalled"] = map[string]any{"module_name": "sales"}
	rows, err := conn.QueryContext(ctx, `SELECT args FROM system.river_job WHERE kind = 'event_delivery' ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	var emitted []event.EventDeliveryArgs
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			t.Fatal(err)
		}
		var args event.EventDeliveryArgs
		if err := json.Unmarshal(raw, &args); err != nil {
			t.Fatal(err)
		}
		emitted = append(emitted, args)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		t.Fatal(err)
	}
	if len(emitted) != len(expected) {
		t.Fatalf("emitted %d events, want %d: %+v", len(emitted), len(expected), emitted)
	}
	var database string
	if err := conn.QueryRowContext(ctx, `SELECT current_database()`).Scan(&database); err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.New(ctx, fmt.Sprintf("postgres://goerp:dev@localhost:15432/%s", database))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	fanoutJobs, err := river.NewClient(riverpgxv5.New(pool), &river.Config{Schema: jobqueue.Schema})
	if err != nil {
		t.Fatal(err)
	}
	worker := eventdelivery.Worker{ModuleRegistry: reg, TenantStore: tenants, Pool: conn}
	for _, args := range emitted {
		var payload map[string]any
		if err := msgpack.Unmarshal(args.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(payload, expected[args.EventName]) {
			t.Errorf("%s payload = %#v, want %#v", args.EventName, payload, expected[args.EventName])
		}
		if args.EventVersion != 0 || args.EmitterModule != "system" || args.UserID != "" || args.TenantID != tt.ID || !args.Transactional {
			t.Errorf("invalid system envelope: %+v", args)
		}
		if err := worker.Work(rivertest.WorkContext(ctx, fanoutJobs), &river.Job[jobqueue.EventDeliveryArgs]{Args: args}); err != nil {
			t.Fatal(err)
		}
		delete(expected, args.EventName)
	}
	var count int
	if err := conn.QueryRowContext(ctx, `SELECT count(*) FROM system.river_job WHERE kind = 'subscriber_delivery' AND args->>'module_name' = 'subscriber' AND args->>'event_version' = '0'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != len(emitted) || len(expected) != 0 {
		t.Fatalf("subscriber jobs = %d for %d events; missing %v", count, len(emitted), expected)
	}
	if err := roles.SeedBuiltinRoles(ctx, tt.Slug); err != nil {
		t.Fatal(err)
	}
	invitations := invite.NewStore(conn, users, roles, nil, nil)
	invitations.SystemEvents = emitter
	if err := invitations.Bootstrap(ctx, tt.Slug); err != nil {
		t.Fatal(err)
	}
	invitation, err := invitations.Invite(ctx, tt.Slug, "invited@example.test", "user", "Invitee", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := invitations.Invite(ctx, tt.Slug, "invited@example.test", "user", "Invitee", nil); err != nil {
		t.Fatal(err)
	}
	invited, err := users.GetByEmail(ctx, "invited@example.test")
	if err != nil {
		t.Fatal(err)
	}
	if err := invitations.Accept(ctx, tt.Slug, invitation.ID, invited.ID, nil); err != nil {
		t.Fatal(err)
	}
	if err := conn.QueryRowContext(ctx, `SELECT count(*) FROM system.river_job WHERE kind = 'event_delivery' AND args->>'event_name' = 'system.user.created'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("created events after registration, invitation, token rotation and acceptance = %d, want 2", count)
	}

}

type failingSystemJobs struct{ err error }

func (f failingSystemJobs) InsertTx(context.Context, *sql.Tx, river.JobArgs, *river.InsertOpts) (*rivertype.JobInsertResult, error) {
	return nil, f.err
}

func TestSystemEvents_RollbackAndPayloadLimit(t *testing.T) {
	conn := membershiptest.New(t)
	tenants := tenant.NewStore(conn)
	tt, err := tenants.CreateTenant(t.Context(), "systemrollback", "Rollback")
	if err != nil {
		t.Fatal(err)
	}
	failure := errors.New("queue unavailable")
	tenants.SystemEvents = event.NewSystemEmitter(conn, failingSystemJobs{failure})
	if _, err := tenants.UpdateStatus(t.Context(), tt.Slug, tenant.StatusActive, nil); !errors.Is(err, failure) {
		t.Fatalf("transition error = %v", err)
	}
	stored, err := tenants.GetByID(t.Context(), tt.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != tenant.StatusProvisioning {
		t.Fatalf("status committed despite queue failure: %s", stored.Status)
	}
	emitter := event.NewSystemEmitter(conn, failingSystemJobs{failure})
	tx, err := conn.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	err = emitter.EmitTx(t.Context(), tx, tt.ID, "system.user.updated", map[string]any{"name": strings.Repeat("x", event.MaxPayloadBytes)})
	if err == nil || !strings.Contains(err.Error(), "limit") || errors.Is(err, failure) {
		t.Fatalf("oversized system payload error = %v", err)
	}
}
