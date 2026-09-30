package notify

import (
	"context"
	"database/sql"
	"encoding/json/v2"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/internal/engine/enginenotif"
	"github.com/djangbahevans/goerp/internal/engine/jobqueue"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/notifconfig"
	"github.com/djangbahevans/goerp/internal/engine/notifications"
	"github.com/djangbahevans/goerp/internal/engine/notiftemplate"
	"github.com/djangbahevans/goerp/internal/engine/providerselect"
	"github.com/djangbahevans/goerp/internal/engine/registry"
	"github.com/djangbahevans/goerp/internal/engine/role"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
	"github.com/djangbahevans/goerp/internal/engine/user"
	"github.com/djangbahevans/goerp/internal/engine/ws"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverdatabasesql"
	"github.com/vmihailenco/msgpack/v5"
)

const localPostgresDSN = "postgres://goerp:dev@localhost:15432/goerp"

const orderConfirmed = "sales.order_confirmed"

var salesTypes = []manifest.NotificationType{
	{
		Name:              "order_confirmed",
		Label:             "Order Confirmed",
		DefaultChannels:   []string{inApp, email, push},
		AvailableChannels: []string{inApp, email, sms, push},
		Templates: map[string]string{
			inApp: "notifications/order_confirmed/in_app.{locale}.json",
			sms:   "notifications/order_confirmed/sms.{locale}.txt",
			push:  "notifications/order_confirmed/push.{locale}.json",
		},
	},
}

type staticConfig struct{ cfg *notifconfig.Config }

func (s staticConfig) Load(context.Context, string) (*notifconfig.Config, error) { return s.cfg, nil }

// staticProviders resolves each category to its map entry, or reports it
// has no installed provider.
type staticProviders map[string]string

func (p staticProviders) Resolve(_ context.Context, _, category string) (string, error) {
	if m, ok := p[category]; ok {
		return m, nil
	}
	return "", providerselect.ErrNoProviderInstalled
}

type broadcast struct {
	channel, tenantID, userID, msgType string
	payload                            any
}

type recordingHub struct {
	mu   sync.Mutex
	sent []broadcast
}

func (h *recordingHub) BroadcastUser(_ context.Context, channel, tenantID, userID, msgType string, payload any) (int, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.sent = append(h.sent, broadcast{channel, tenantID, userID, msgType, payload})
	return 1, nil
}

type testEnv struct {
	conn   *sql.DB
	roleID string
	tenant *tenant.Tenant
	store  *notifications.Store
	config *notifconfig.Config
	hub    *recordingHub
	sender *Sender
}

func openTestEnv(t *testing.T) *testEnv {
	t.Helper()
	ctx := t.Context()

	conn, err := db.New(localPostgresDSN)
	if err != nil {
		t.Skipf("postgres not reachable at %s (start compose.dev.yml): %v", localPostgresDSN, err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	tenantStore := tenant.NewStore(conn)
	if err := tenantStore.Bootstrap(ctx); err != nil {
		t.Fatalf("tenant Bootstrap() error: %v", err)
	}
	if err := user.NewStore(conn).Bootstrap(ctx); err != nil {
		t.Fatalf("user Bootstrap() error: %v", err)
	}
	pgxPool, err := db.NewPgxPool(ctx, localPostgresDSN)
	if err != nil {
		t.Fatalf("NewPgxPool() error: %v", err)
	}
	t.Cleanup(pgxPool.Close)
	if err := jobqueue.Migrate(ctx, pgxPool); err != nil {
		t.Fatalf("jobqueue.Migrate() error: %v", err)
	}
	jobs, err := river.NewClient(riverdatabasesql.New(conn), &river.Config{Schema: jobqueue.Schema})
	if err != nil {
		t.Fatalf("river.NewClient() error: %v", err)
	}

	slug := fmt.Sprintf("notifytest%d", time.Now().UnixNano())
	tt, err := tenantStore.CreateTenant(ctx, slug, "Notify Test Co")
	if err != nil {
		t.Fatalf("CreateTenant(%q) error: %v", slug, err)
	}
	t.Cleanup(func() {
		_, _ = conn.Exec("DELETE FROM system.river_job WHERE args->>'tenant_id' = $1", tt.ID)
		_, _ = conn.Exec("DELETE FROM system.tenants WHERE id = $1", tt.ID)
	})
	schema := tenantschema.Name(slug)
	if _, err := conn.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatalf("create tenant schema: %v", err)
	}
	t.Cleanup(func() { _, _ = conn.Exec("DROP SCHEMA IF EXISTS " + schema + " CASCADE") })

	roles := role.NewStore(conn)
	store := notifications.NewStore(conn)
	for _, bootstrap := range []func(context.Context, string) error{
		roles.Bootstrap, store.BootstrapFeed, store.BootstrapDeliveries, store.BootstrapPreferences, store.BootstrapDeviceTokens,
		store.BootstrapTemplates,
	} {
		if err := bootstrap(ctx, slug); err != nil {
			t.Fatalf("bootstrap notification tables: %v", err)
		}
	}

	var roleID string
	if err := conn.QueryRowContext(ctx, `INSERT INTO `+schema+`.roles (name) VALUES ('member') RETURNING id`).Scan(&roleID); err != nil {
		t.Fatalf("create role: %v", err)
	}

	cfg := &notifconfig.Config{
		EmailEnabled: true, SMSEnabled: true, PushEnabled: true,
		Email: notifconfig.EmailConfig{Provider: notifconfig.ProviderResend, APIKey: "re_test"},
		SMS:   notifconfig.SMSConfig{SenderID: "ACME"},
	}
	hub := &recordingHub{}
	reg := salesRegistry(t)
	sender := NewSender(Deps{
		DB:        conn,
		Store:     store,
		Registry:  reg,
		Config:    staticConfig{cfg},
		Providers: staticProviders{providerselect.CategoryPush: "connector_fcm", providerselect.CategorySMS: "connector_africastalking"},
		Tenants:   tenantStore,
		Members:   roles,
		Jobs:      jobs,
		Hub:       hub,
	})
	env := &testEnv{conn: conn, roleID: roleID, tenant: tt, store: store, config: cfg, hub: hub, sender: sender}
	engineRows, err := enginenotif.DefaultRows()
	if err != nil {
		t.Fatalf("enginenotif.DefaultRows() error: %v", err)
	}
	if err := store.SeedDefaultTemplates(ctx, slug, EngineModule, engineRows); err != nil {
		t.Fatalf("seed engine templates: %v", err)
	}
	env.seedTemplates(t, reg)
	return env
}

// seedTemplates stores the templates of reg's modules as the tenant's
// default notification_templates rows, the way a module install does.
func (e *testEnv) seedTemplates(t *testing.T, reg *registry.ModuleRegistry) {
	t.Helper()
	for name, mod := range reg.Snapshot().Modules() {
		rows, err := mod.NotifTemplates.Rows(name)
		if err != nil {
			t.Fatalf("%s template rows: %v", name, err)
		}
		if err := e.store.SeedDefaultTemplates(t.Context(), e.tenant.Slug, name, rows); err != nil {
			t.Fatalf("seed %s templates: %v", name, err)
		}
	}
}

// salesRegistry holds a "sales" module declaring salesTypes, with in_app,
// sms and push templates.
func salesRegistry(t *testing.T) *registry.ModuleRegistry {
	t.Helper()
	dir := t.TempDir()
	base := filepath.Join(dir, "notifications", "order_confirmed")
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, template := range map[string]string{
		"in_app.en.json": `{"title": "Order {{.OrderReference}} confirmed", "body": "Hi {{.UserFirstName}}, {{.TenantName}} confirmed it.{{if .BreakInApp}}{{index .BreakInApp 9}}{{end}}", "action_url": "/_m/sales/orders/{{.OrderID}}", "icon": "shopping-cart"}`,
		"sms.en.txt":     "{{.TenantName}}: order {{.OrderReference}} confirmed.{{if .BreakSMS}}{{index .BreakSMS 9}}{{end}}\n",
		"push.en.json":   `{"title": "Order {{.OrderReference}}", "body": "Confirmed by {{.TenantName}}"}`,
	} {
		if err := os.WriteFile(filepath.Join(base, name), []byte(template), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	templates, err := notiftemplate.Load(salesTypes, dir)
	if err != nil {
		t.Fatalf("notiftemplate.Load() error: %v", err)
	}

	reg := &registry.ModuleRegistry{}
	if _, err := reg.Update(map[string]*module.LoadedModule{
		"sales": {
			Status:         module.StatusReady,
			Manifest:       manifest.Manifest{Name: "sales", Type: "standard", Version: "1.0.0", NotificationTypes: salesTypes},
			NotifTemplates: templates,
		},
	}); err != nil {
		t.Fatalf("registry Update() error: %v", err)
	}
	return reg
}

// createUser inserts a member of the tenant with a profile, returning its
// ID.
func (e *testEnv) createUser(t *testing.T, name, phone string) string {
	t.Helper()
	id := e.createOutsider(t, name)
	if err := role.NewStore(e.conn).AddMember(t.Context(), e.tenant.Slug, id); err != nil {
		t.Fatalf("AddMember() error: %v", err)
	}
	if err := role.NewStore(e.conn).AssignRole(t.Context(), e.tenant.Slug, id, e.roleID, ""); err != nil {
		t.Fatalf("AssignRole() error: %v", err)
	}
	if phone != "" {
		if err := role.NewStore(e.conn).UpdateMemberProfile(t.Context(), e.tenant.Slug, id, role.MemberProfileUpdate{SetPhone: true, Phone: &phone}); err != nil {
			t.Fatalf("UpdateMemberProfile() error: %v", err)
		}
	}
	return id
}

// createOutsider inserts a user with a profile who is not a member of the
// tenant, returning its ID. A phone belongs to a membership, so an outsider
// has none.
func (e *testEnv) createOutsider(t *testing.T, name string) string {
	t.Helper()
	var id string
	addr := fmt.Sprintf("notify%d@example.test", time.Now().UnixNano())
	if err := e.conn.QueryRow(`INSERT INTO system.users (email) VALUES ($1) RETURNING id`, addr).Scan(&id); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	t.Cleanup(func() { _, _ = e.conn.Exec("DELETE FROM system.users WHERE id = $1", id) })
	if _, err := e.conn.Exec(`INSERT INTO system.user_profiles (user_id, name) VALUES ($1, $2)`, id, name); err != nil {
		t.Fatalf("insert user profile: %v", err)
	}
	return id
}

func (e *testEnv) registerDevice(t *testing.T, userID, platform, token string) {
	t.Helper()
	if err := e.store.RegisterDeviceToken(t.Context(), e.tenant.Slug, e.tenant.ID, userID, platform, token, ""); err != nil {
		t.Fatalf("RegisterDeviceToken() error: %v", err)
	}
}

type deliveryRow struct {
	channel, recipient, status, provider string
	delivered                            bool
}

func (e *testEnv) deliveries(t *testing.T) []deliveryRow {
	t.Helper()
	rows, err := e.conn.Query(fmt.Sprintf(`
		SELECT channel, recipient, status, COALESCE(provider, ''), delivered_at IS NOT NULL
		FROM %s.notification_deliveries ORDER BY channel, recipient
	`, tenantschema.Name(e.tenant.Slug)))
	if err != nil {
		t.Fatalf("query deliveries: %v", err)
	}
	defer rows.Close()
	var out []deliveryRow
	for rows.Next() {
		var d deliveryRow
		if err := rows.Scan(&d.channel, &d.recipient, &d.status, &d.provider, &d.delivered); err != nil {
			t.Fatalf("scan delivery: %v", err)
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("query deliveries: %v", err)
	}
	return out
}

func (e *testEnv) count(t *testing.T, query string, args ...any) int {
	t.Helper()
	var n int
	if err := e.conn.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}

func (e *testEnv) notificationCount(t *testing.T) int {
	return e.count(t, "SELECT count(*) FROM "+tenantschema.Name(e.tenant.Slug)+".notifications")
}

func (e *testEnv) deliveryCount(t *testing.T) int {
	return e.count(t, "SELECT count(*) FROM "+tenantschema.Name(e.tenant.Slug)+".notification_deliveries")
}

func (e *testEnv) jobCount(t *testing.T) int {
	return e.count(t, "SELECT count(*) FROM system.river_job WHERE args->>'tenant_id' = $1", e.tenant.ID)
}

func TestSend_CreatesNotificationDeliveriesAndJobs(t *testing.T) {
	env := openTestEnv(t)
	userID := env.createUser(t, "Ama Owusu", "")
	env.registerDevice(t, userID, "android", "tok-a")
	env.registerDevice(t, userID, "ios", "tok-b")

	res := env.sendParked(t, userID, map[string]any{"OrderReference": `ORD "42"`, "OrderID": "o-42"}, Options{TraceID: "trace-1"})
	if want := []string{inApp, email, push}; !slices.Equal(res.ChannelsUsed, want) {
		t.Errorf("ChannelsUsed = %v, want %v", res.ChannelsUsed, want)
	}

	var title, body, actionURL, icon, typ, mod string
	err := env.conn.QueryRow(fmt.Sprintf(`SELECT title, body, action_url, icon, type, module FROM %s.notifications WHERE id = $1`, tenantschema.Name(env.tenant.Slug)), res.NotificationID).
		Scan(&title, &body, &actionURL, &icon, &typ, &mod)
	if err != nil {
		t.Fatalf("load notification: %v", err)
	}
	if title != `Order ORD "42" confirmed` || body != "Hi Ama, Notify Test Co confirmed it." || actionURL != "/_m/sales/orders/o-42" || icon != "shopping-cart" {
		t.Errorf("rendered row = %q / %q / %q / %q", title, body, actionURL, icon)
	}
	if typ != orderConfirmed || mod != "sales" {
		t.Errorf("type/module = %q/%q", typ, mod)
	}

	var address string
	if err := env.conn.QueryRow(`SELECT email FROM system.users WHERE id = $1`, userID).Scan(&address); err != nil {
		t.Fatal(err)
	}
	want := []deliveryRow{
		{channel: email, recipient: address, status: notifications.DeliveryPending, provider: notifconfig.ProviderResend},
		{channel: inApp, recipient: userID, status: notifications.DeliveryDelivered, delivered: true},
		{channel: push, recipient: "tok-a", status: notifications.DeliveryPending, provider: "connector_fcm"},
		{channel: push, recipient: "tok-b", status: notifications.DeliveryPending, provider: "connector_fcm"},
	}
	if got := env.deliveries(t); !slices.Equal(got, want) {
		t.Errorf("deliveries = %+v\nwant %+v", got, want)
	}

	var emailArgs jobqueue.EmailSendArgs
	var emailQueue string
	var emailArgsRaw []byte
	if err := env.conn.QueryRow(`SELECT queue, args FROM system.river_job WHERE kind = 'email_send' AND args->>'tenant_id' = $1`, env.tenant.ID).Scan(&emailQueue, &emailArgsRaw); err != nil {
		t.Fatalf("load email_send job: %v", err)
	}
	if err := json.Unmarshal(emailArgsRaw, &emailArgs); err != nil {
		t.Fatal(err)
	}
	if emailQueue != jobqueue.QueueEmail || emailArgs.NotificationID != res.NotificationID || emailArgs.Recipient != address ||
		emailArgs.IdempotencyKey != res.NotificationID+":email:"+address || emailArgs.DeliveryID == "" {
		t.Errorf("email_send job = %s %+v", emailQueue, emailArgs)
	}

	var pushArgs jobqueue.WASMJobArgs
	var pushArgsRaw []byte
	if err := env.conn.QueryRow(`SELECT args FROM system.river_job WHERE kind = 'wasm_job' AND args->>'tenant_id' = $1`, env.tenant.ID).Scan(&pushArgsRaw); err != nil {
		t.Fatalf("load push_send job: %v", err)
	}
	if err := json.Unmarshal(pushArgsRaw, &pushArgs); err != nil {
		t.Fatal(err)
	}
	if pushArgs.ModuleName != "connector_fcm" || pushArgs.JobType != JobTypePushSend || pushArgs.ProviderCategory != providerselect.CategoryPush ||
		pushArgs.EnqueuedBy != "sales" || pushArgs.TraceID != "trace-1" {
		t.Errorf("push_send job = %+v", pushArgs)
	}
	if pushArgs.NotificationID != res.NotificationID {
		t.Errorf("push_send NotificationID = %q, want %q", pushArgs.NotificationID, res.NotificationID)
	}
	var payload abiv1.PushSendPayload
	if err := msgpack.Unmarshal(pushArgs.Payload, &payload); err != nil {
		t.Fatalf("decode push_send payload: %v", err)
	}
	wantPush := abiv1.PushSendPayload{
		SchemaVersion: abiv1.ProviderPayloadSchemaVersion, TenantID: env.tenant.ID, NotificationID: res.NotificationID,
		Tokens: []abiv1.PushDeviceToken{
			{Platform: "android", Token: "tok-a", IdempotencyKey: res.NotificationID + ":push:tok-a"},
			{Platform: "ios", Token: "tok-b", IdempotencyKey: res.NotificationID + ":push:tok-b"},
		},
		Title: `Order ORD "42"`, Body: "Confirmed by Notify Test Co", ActionURL: "/_m/sales/orders/o-42",
		Data: map[string]string{"notification_id": res.NotificationID, "type": orderConfirmed},
	}
	if !reflect.DeepEqual(payload, wantPush) {
		t.Errorf("push_send payload = %+v\nwant %+v", payload, wantPush)
	}
	if n := env.jobCount(t); n != 2 {
		t.Errorf("%d jobs enqueued, want 2", n)
	}

	if len(env.hub.sent) != 1 {
		t.Fatalf("%d broadcasts, want 1", len(env.hub.sent))
	}
	b := env.hub.sent[0]
	event, _ := b.payload.(feedEvent)
	if b.channel != ws.NotificationsChannel || b.tenantID != env.tenant.ID || b.userID != userID || b.msgType != "notification.new" ||
		event.ID != res.NotificationID || event.Title != title {
		t.Errorf("broadcast = %+v", b)
	}
}

func TestSend_SMSGoesToTheProfilePhoneThroughTheSMSProvider(t *testing.T) {
	env := openTestEnv(t)
	userID := env.createUser(t, "Kofi Mensah", "+233501234567")

	res, err := env.sender.Send(t.Context(), env.tenant.ID, "sales", orderConfirmed, userID,
		map[string]any{"OrderReference": "ORD-7"}, Options{ForceChannels: []string{sms}})
	if err != nil {
		t.Fatalf("Send() error: %v", err)
	}
	if !slices.Contains(res.ChannelsUsed, sms) {
		t.Fatalf("ChannelsUsed = %v, want sms", res.ChannelsUsed)
	}

	var raw []byte
	if err := env.conn.QueryRow(`SELECT args FROM system.river_job WHERE kind = 'wasm_job' AND args->>'tenant_id' = $1 AND args->>'job_type' = 'sms_send'`, env.tenant.ID).Scan(&raw); err != nil {
		t.Fatalf("load sms_send job: %v", err)
	}
	var args jobqueue.WASMJobArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		t.Fatal(err)
	}
	var payload abiv1.SMSSendPayload
	if err := msgpack.Unmarshal(args.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if args.ModuleName != "connector_africastalking" || args.ProviderCategory != providerselect.CategorySMS || args.NotificationID != res.NotificationID {
		t.Errorf("sms_send job = %+v", args)
	}
	want := abiv1.SMSSendPayload{
		SchemaVersion: abiv1.ProviderPayloadSchemaVersion, TenantID: env.tenant.ID, NotificationID: res.NotificationID,
		To: "+233501234567", From: "ACME", Body: "Notify Test Co: order ORD-7 confirmed.",
		IdempotencyKey: res.NotificationID + ":sms:+233501234567",
	}
	if payload != want {
		t.Errorf("sms_send payload = %+v\nwant %+v", payload, want)
	}
}

func TestSend_DropsChannelsThatCannotBeDelivered(t *testing.T) {
	env := openTestEnv(t)
	// No phone, no device tokens, and no email provider configured.
	env.config.Email = notifconfig.EmailConfig{Provider: notifconfig.ProviderSMTP}
	userID := env.createUser(t, "Esi", "")

	res, err := env.sender.Send(t.Context(), env.tenant.ID, "sales", orderConfirmed, userID, nil, Options{ForceChannels: []string{sms}})
	if err != nil {
		t.Fatalf("Send() error: %v", err)
	}
	if want := []string{inApp}; !slices.Equal(res.ChannelsUsed, want) {
		t.Errorf("ChannelsUsed = %v, want %v", res.ChannelsUsed, want)
	}
	if n := env.jobCount(t); n != 0 {
		t.Errorf("%d jobs enqueued, want 0", n)
	}
}

func TestSendTx_RollbackLeavesNothing(t *testing.T) {
	env := openTestEnv(t)
	userID := env.createUser(t, "Yaw", "")
	env.registerDevice(t, userID, "web", "tok-web")

	tx, err := env.conn.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	res, err := env.sender.SendTx(t.Context(), tx, env.tenant.ID, "sales", orderConfirmed, userID, nil, Options{})
	if err != nil {
		t.Fatalf("SendTx() error: %v", err)
	}
	if len(res.ChannelsUsed) != 3 {
		t.Fatalf("ChannelsUsed = %v, want in_app, email and push", res.ChannelsUsed)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}

	if n := env.notificationCount(t); n != 0 {
		t.Errorf("%d notifications after rollback, want 0", n)
	}
	if n := env.deliveryCount(t); n != 0 {
		t.Errorf("%d deliveries after rollback, want 0", n)
	}
	if n := env.jobCount(t); n != 0 {
		t.Errorf("%d jobs after rollback, want 0", n)
	}
	if len(env.hub.sent) != 0 {
		t.Errorf("SendTx broadcast %d messages, want none before commit", len(env.hub.sent))
	}
}

func TestSend_KillSwitchBeatsPreferenceAndForcedChannel(t *testing.T) {
	env := openTestEnv(t)
	env.config.EmailEnabled = false
	userID := env.createUser(t, "Abena", "")
	emailOn := true
	if err := env.store.UpdatePreferences(t.Context(), env.tenant.Slug, env.tenant.ID, userID, nil,
		map[string]notifications.ChannelsPatch{orderConfirmed: {Email: &emailOn}}); err != nil {
		t.Fatalf("UpdatePreferences() error: %v", err)
	}

	res, err := env.sender.Send(t.Context(), env.tenant.ID, "sales", orderConfirmed, userID, nil,
		Options{ForceChannels: []string{email}, AdditionalChannels: []string{email}})
	if err != nil {
		t.Fatalf("Send() error: %v", err)
	}
	if slices.Contains(res.ChannelsUsed, email) {
		t.Errorf("ChannelsUsed = %v, contains email despite the kill switch", res.ChannelsUsed)
	}
	if n := env.count(t, `SELECT count(*) FROM system.river_job WHERE kind = 'email_send' AND args->>'tenant_id' = $1`, env.tenant.ID); n != 0 {
		t.Errorf("%d email_send jobs, want 0", n)
	}
}

func TestSend_InAppIsDeliveredEvenWithEveryPreferenceOff(t *testing.T) {
	env := openTestEnv(t)
	userID := env.createUser(t, "Kwame", "")
	off := false
	if err := env.store.UpdatePreferences(t.Context(), env.tenant.Slug, env.tenant.ID, userID,
		&notifications.ChannelsPatch{Email: &off, SMS: &off, Push: &off}, nil); err != nil {
		t.Fatalf("UpdatePreferences() error: %v", err)
	}

	res, err := env.sender.Send(t.Context(), env.tenant.ID, "sales", orderConfirmed, userID, nil, Options{})
	if err != nil {
		t.Fatalf("Send() error: %v", err)
	}
	if want := []string{inApp}; !slices.Equal(res.ChannelsUsed, want) {
		t.Errorf("ChannelsUsed = %v, want %v", res.ChannelsUsed, want)
	}
	want := []deliveryRow{{channel: inApp, recipient: userID, status: notifications.DeliveryDelivered, delivered: true}}
	if got := env.deliveries(t); !slices.Equal(got, want) {
		t.Errorf("deliveries = %+v, want %+v", got, want)
	}
	if n := env.jobCount(t); n != 0 {
		t.Errorf("%d jobs enqueued for in_app, want 0", n)
	}
}

func TestSend_EngineTypeSkipsManifestValidation(t *testing.T) {
	env := openTestEnv(t)
	userID := env.createUser(t, "Adjoa", "")

	data := map[string]any{"AuthorName": "Kwame Mensah", "RecordName": "SO-0007", "Excerpt": "Can you confirm?"}
	res, err := env.sender.Send(t.Context(), env.tenant.ID, EngineModule, "engine.record_mention", userID, data, Options{})
	if err != nil {
		t.Fatalf("Send() error: %v", err)
	}
	var title string
	if err := env.conn.QueryRow(fmt.Sprintf(`SELECT title FROM %s.notifications WHERE id = $1`, tenantschema.Name(env.tenant.Slug)), res.NotificationID).Scan(&title); err != nil {
		t.Fatal(err)
	}
	if title != "Kwame Mensah mentioned you on SO-0007" {
		t.Errorf("title = %q, want the engine type's own template rendered", title)
	}
}

func TestSend_EngineTypeRendersItsEmbeddedTemplate(t *testing.T) {
	env := openTestEnv(t)
	userID := env.createUser(t, "Adwoa", "")

	data := map[string]any{"Summary": "Call back", "TypeLabel": "Call", "TypeIcon": "phone", "RecordName": "SO-0007", "DueDate": "2026-09-25", "Overdue": false}
	res, err := env.sender.Send(t.Context(), env.tenant.ID, EngineModule, "engine.activity_due", userID, data, Options{ActionURL: "/_m/sales/orders/o7"})
	if err != nil {
		t.Fatalf("Send() error: %v", err)
	}
	var title, body, actionURL, icon string
	if err := env.conn.QueryRow(fmt.Sprintf(`SELECT title, body, action_url, icon FROM %s.notifications WHERE id = $1`, tenantschema.Name(env.tenant.Slug)), res.NotificationID).Scan(&title, &body, &actionURL, &icon); err != nil {
		t.Fatal(err)
	}
	if title != "Due today: Call back" || body != "Call on SO-0007, due 2026-09-25" || actionURL != "/_m/sales/orders/o7" || icon != "phone" {
		t.Errorf("notification = (%q, %q, %q, %q), want the rendered activity_due template", title, body, actionURL, icon)
	}
}

func TestSend_RejectsBadRequestsWithoutWriting(t *testing.T) {
	env := openTestEnv(t)
	userID := env.createUser(t, "Akua", "")
	outsiderID := env.createOutsider(t, "Nana")

	tests := []struct {
		name             string
		module, typ, uid string
		opts             Options
		want             error
	}{
		{"undeclared module type", "sales", "sales.order_shipped", userID, Options{}, ErrUndeclaredType},
		{"another module's type", "crm", orderConfirmed, userID, Options{}, ErrUndeclaredType},
		{"module type sent as the engine", EngineModule, orderConfirmed, userID, Options{}, ErrUndeclaredType},
		{"unknown engine type", EngineModule, "engine.order_confirmed", userID, Options{}, ErrUndeclaredType},
		{"unknown channel", "sales", orderConfirmed, userID, Options{ForceChannels: []string{"fax"}}, ErrUnknownChannel},
		{"invalid priority", "sales", orderConfirmed, userID, Options{Priority: "urgent"}, ErrInvalidPriority},
		{"unknown user", "sales", orderConfirmed, "00000000-0000-0000-0000-000000000000", Options{}, ErrUnknownUser},
		{"malformed user ID", "sales", orderConfirmed, "not-a-uuid", Options{}, ErrUnknownUser},
		{"user of another tenant", "sales", orderConfirmed, outsiderID, Options{}, ErrUnknownUser},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := env.sender.Send(t.Context(), env.tenant.ID, tt.module, tt.typ, tt.uid, nil, tt.opts)
			if !errors.Is(err, tt.want) {
				t.Errorf("Send() error = %v, want %v", err, tt.want)
			}
		})
	}
	if n := env.notificationCount(t); n != 0 {
		t.Errorf("%d notifications written, want 0", n)
	}
}

func TestSend_IdempotencyKeySendsOncePerUser(t *testing.T) {
	env := openTestEnv(t)
	ama := env.createUser(t, "Ama Owusu", "")
	kofi := env.createUser(t, "Kofi Mensah", "")
	opts := Options{IdempotencyKey: "order-confirmed:o-1"}

	first, err := env.sender.Send(t.Context(), env.tenant.ID, "sales", orderConfirmed, ama, nil, opts)
	if err != nil {
		t.Fatalf("first Send() error: %v", err)
	}
	feedRows, deliveries, jobs, pushes := env.notificationCount(t), env.deliveryCount(t), env.jobCount(t), len(env.hub.sent)

	again, err := env.sender.Send(t.Context(), env.tenant.ID, "sales", orderConfirmed, ama, nil, opts)
	if err != nil {
		t.Fatalf("repeated Send() error: %v", err)
	}
	if !again.Deduplicated || again.NotificationID != first.NotificationID || !slices.Equal(again.ChannelsUsed, first.ChannelsUsed) {
		t.Errorf("repeated Send() = %+v, want the first send's notification %s %v deduplicated", again, first.NotificationID, first.ChannelsUsed)
	}
	if env.notificationCount(t) != feedRows || env.deliveryCount(t) != deliveries || env.jobCount(t) != jobs || len(env.hub.sent) != pushes {
		t.Error("a deduplicated Send() wrote a notification, delivery or job, or pushed notification.new")
	}

	other, err := env.sender.Send(t.Context(), env.tenant.ID, "sales", orderConfirmed, kofi, nil, opts)
	if err != nil {
		t.Fatalf("Send() to another user error: %v", err)
	}
	if other.Deduplicated {
		t.Error("the same idempotency key deduplicated a send to a different user")
	}
}

func TestSendTx_IdempotencyKeyFromARolledBackSendIsFree(t *testing.T) {
	env := openTestEnv(t)
	userID := env.createUser(t, "Ama Owusu", "")
	opts := Options{IdempotencyKey: "k"}

	tx, err := env.conn.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := env.sender.SendTx(t.Context(), tx, env.tenant.ID, "sales", orderConfirmed, userID, nil, opts); err != nil {
		t.Fatalf("SendTx() error: %v", err)
	}
	_ = tx.Rollback()

	res, err := env.sender.Send(t.Context(), env.tenant.ID, "sales", orderConfirmed, userID, nil, opts)
	if err != nil {
		t.Fatalf("Send() error: %v", err)
	}
	if res.Deduplicated {
		t.Error("a key used only by a rolled-back send deduplicated a later send")
	}
}

func TestSendBulk_SendsToEachUserOnce(t *testing.T) {
	env := openTestEnv(t)
	ama := env.createUser(t, "Ama Owusu", "")
	kofi := env.createUser(t, "Kofi Mensah", "")

	results, err := env.sender.SendBulk(t.Context(), env.tenant.ID, "sales", orderConfirmed, []string{ama, kofi, strings.ToUpper(ama)},
		map[string]any{"OrderReference": "ORD-7"}, Options{})
	if err != nil {
		t.Fatalf("SendBulk() error: %v", err)
	}
	if len(results) != 2 || results[0].UserID != ama || results[1].UserID != kofi {
		t.Fatalf("SendBulk() results = %+v, want one each for %s and %s in order", results, ama, kofi)
	}
	if n := env.notificationCount(t); n != 2 {
		t.Errorf("notifications = %d, want 2", n)
	}
	if n := len(env.hub.sent); n != 2 {
		t.Errorf("notification.new pushes = %d, want 2", n)
	}
}

func TestSendBulk_OneBadRecipientSendsNothing(t *testing.T) {
	env := openTestEnv(t)
	ama := env.createUser(t, "Ama Owusu", "")
	outsider := env.createOutsider(t, "Yaw Boateng")

	_, err := env.sender.SendBulk(t.Context(), env.tenant.ID, "sales", orderConfirmed, []string{ama, outsider}, nil, Options{})
	if !errors.Is(err, ErrUnknownUser) {
		t.Fatalf("SendBulk() error = %v, want ErrUnknownUser", err)
	}
	if n := env.notificationCount(t); n != 0 {
		t.Errorf("notifications = %d, want 0", n)
	}
}

func TestSendBulk_RejectsTooManyRecipients(t *testing.T) {
	env := openTestEnv(t)
	userIDs := make([]string, MaxBulkRecipients+1)
	for i := range userIDs {
		userIDs[i] = fmt.Sprintf("00000000-0000-0000-0000-%012d", i)
	}
	if _, err := env.sender.SendBulk(t.Context(), env.tenant.ID, "sales", orderConfirmed, userIDs, nil, Options{}); !errors.Is(err, ErrTooManyRecipients) {
		t.Fatalf("SendBulk() error = %v, want ErrTooManyRecipients", err)
	}
}
