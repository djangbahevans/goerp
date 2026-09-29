package notify

import (
	"context"
	"database/sql"
	"encoding/json/v2"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/db"
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
		Templates:         map[string]string{inApp: "notifications/order_confirmed/in_app.{locale}.json"},
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
	sender := NewSender(Deps{
		DB:        conn,
		Store:     store,
		Registry:  salesRegistry(t),
		Config:    staticConfig{cfg},
		Providers: staticProviders{providerselect.CategoryPush: "connector_fcm", providerselect.CategorySMS: "connector_africastalking"},
		Tenants:   tenantStore,
		Members:   roles,
		Jobs:      jobs,
		Hub:       hub,
	})
	return &testEnv{conn: conn, roleID: roleID, tenant: tt, store: store, config: cfg, hub: hub, sender: sender}
}

// salesRegistry holds a "sales" module declaring salesTypes, with an
// in_app template.
func salesRegistry(t *testing.T) *registry.ModuleRegistry {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "notifications", "order_confirmed", "in_app.en.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	template := `{"title": "Order {{.OrderReference}} confirmed", "body": "Hi {{.UserFirstName}}, {{.TenantName}} confirmed it.", "action_url": "/_m/sales/orders/{{.OrderID}}", "icon": "shopping-cart"}`
	if err := os.WriteFile(path, []byte(template), 0o644); err != nil {
		t.Fatal(err)
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
	id := e.createOutsider(t, name, phone)
	if err := role.NewStore(e.conn).AssignRole(t.Context(), e.tenant.Slug, id, e.roleID, ""); err != nil {
		t.Fatalf("AssignRole() error: %v", err)
	}
	return id
}

// createOutsider inserts a user with a profile who is not a member of the
// tenant, returning its ID.
func (e *testEnv) createOutsider(t *testing.T, name, phone string) string {
	t.Helper()
	var id string
	addr := fmt.Sprintf("notify%d@example.test", time.Now().UnixNano())
	if err := e.conn.QueryRow(`INSERT INTO system.users (email) VALUES ($1) RETURNING id`, addr).Scan(&id); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	t.Cleanup(func() { _, _ = e.conn.Exec("DELETE FROM system.users WHERE id = $1", id) })
	if _, err := e.conn.Exec(`INSERT INTO system.user_profiles (user_id, name, phone) VALUES ($1, $2, NULLIF($3, ''))`, id, name, phone); err != nil {
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

	res, err := env.sender.Send(t.Context(), env.tenant.ID, "sales", orderConfirmed, userID,
		map[string]any{"OrderReference": `ORD "42"`, "OrderID": "o-42"}, Options{TraceID: "trace-1"})
	if err != nil {
		t.Fatalf("Send() error: %v", err)
	}
	if want := []string{inApp, email, push}; !slices.Equal(res.ChannelsUsed, want) {
		t.Errorf("ChannelsUsed = %v, want %v", res.ChannelsUsed, want)
	}

	var title, body, actionURL, icon, typ, mod string
	err = env.conn.QueryRow(fmt.Sprintf(`SELECT title, body, action_url, icon, type, module FROM %s.notifications WHERE id = $1`, tenantschema.Name(env.tenant.Slug)), res.NotificationID).
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
	var payload pushSendPayload
	if err := msgpack.Unmarshal(pushArgs.Payload, &payload); err != nil {
		t.Fatalf("decode push_send payload: %v", err)
	}
	if payload.NotificationID != res.NotificationID || len(payload.Tokens) != 2 ||
		payload.Tokens[0] != (pushToken{Platform: "android", Token: "tok-a", IdempotencyKey: res.NotificationID + ":push:tok-a"}) {
		t.Errorf("push_send payload = %+v", payload)
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

	res, err := env.sender.Send(t.Context(), env.tenant.ID, "sales", orderConfirmed, userID, nil, Options{ForceChannels: []string{sms}})
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
	var payload smsSendPayload
	if err := msgpack.Unmarshal(args.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if args.ModuleName != "connector_africastalking" || payload.To != "+233501234567" || payload.From != "ACME" ||
		payload.IdempotencyKey != res.NotificationID+":sms:+233501234567" || payload.SchemaVersion != providerPayloadSchemaVersion {
		t.Errorf("sms_send job = %+v, payload %+v", args, payload)
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

	res, err := env.sender.Send(t.Context(), env.tenant.ID, EngineModule, "engine.activity_due", userID, nil, Options{})
	if err != nil {
		t.Fatalf("Send() error: %v", err)
	}
	var title string
	if err := env.conn.QueryRow(fmt.Sprintf(`SELECT title FROM %s.notifications WHERE id = $1`, tenantschema.Name(env.tenant.Slug)), res.NotificationID).Scan(&title); err != nil {
		t.Fatal(err)
	}
	if title != "Activity due today" {
		t.Errorf("title = %q, want the type's label", title)
	}
}

func TestSend_RejectsBadRequestsWithoutWriting(t *testing.T) {
	env := openTestEnv(t)
	userID := env.createUser(t, "Akua", "")
	outsiderID := env.createOutsider(t, "Nana", "")

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
