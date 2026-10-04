package notify

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
	"uuid"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/jobqueue"
	"github.com/djangbahevans/goerp/internal/engine/notifications"
	"github.com/djangbahevans/goerp/internal/engine/role"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverdatabasesql"
	"github.com/vmihailenco/msgpack/v5"
)

type readQueryCounter struct {
	mu      sync.Mutex
	queries []string
}

func (c *readQueryCounter) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	if strings.HasPrefix(strings.ToUpper(strings.TrimSpace(data.SQL)), "SELECT") {
		c.mu.Lock()
		c.queries = append(c.queries, data.SQL)
		c.mu.Unlock()
	}

	return ctx
}

func (*readQueryCounter) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func (c *readQueryCounter) reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.queries = nil
}

func (c *readQueryCounter) reads() []string {
	c.mu.Lock()
	defer c.mu.Unlock()

	return slices.Clone(c.queries)
}

func (e *testEnv) countSenderReads(t *testing.T) *readQueryCounter {
	t.Helper()
	cfg, err := pgx.ParseConfig(localPostgresDSN)
	if err != nil {
		t.Fatal(err)
	}
	counter := &readQueryCounter{}
	cfg.Tracer = counter
	conn := stdlib.OpenDB(*cfg)
	t.Cleanup(func() { _ = conn.Close() })

	jobs, err := river.NewClient(riverdatabasesql.New(conn), &river.Config{Schema: jobqueue.Schema})
	if err != nil {
		t.Fatal(err)
	}
	e.sender.DB = conn
	e.sender.Store = notifications.NewStore(conn)
	e.sender.Members = role.NewStore(conn)
	e.sender.Tenants = tenant.NewStore(conn)
	e.sender.Jobs = jobs

	return counter
}

func (e *testEnv) createBulkUsers(t *testing.T, count int) []string {
	t.Helper()
	ctx := t.Context()
	rows, err := e.conn.QueryContext(ctx, `
		INSERT INTO system.users (email)
		SELECT 'notifybatch-' || $1 || '-' || i || '@example.test'
		FROM generate_series(1, $2::int) i
		RETURNING id
	`, e.tenant.ID, count)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()

	ids := make([]string, 0, count)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = e.conn.Exec(`DELETE FROM system.users WHERE id = ANY($1::uuid[])`, ids) })

	schema := tenantschema.Name(e.tenant.Slug)
	for _, query := range []string{
		`INSERT INTO system.user_profiles (user_id, name, locale)
		 SELECT id, 'Recipient ' || i, 'fr-x-' || i FROM unnest($1::uuid[]) WITH ORDINALITY u(id, i)`,
		`INSERT INTO ` + schema + `.tenant_members (user_id) SELECT unnest($1::uuid[])`,
		`INSERT INTO ` + schema + `.user_roles (user_id, role_id) SELECT unnest($1::uuid[]), '` + e.roleID + `'::uuid`,
		`INSERT INTO ` + schema + `.user_device_tokens (tenant_id, user_id, platform, token)
		 SELECT '` + e.tenant.ID + `'::uuid, id, 'android', 'token:' || id FROM unnest($1::uuid[]) u(id)`,
	} {
		if _, err := e.conn.ExecContext(ctx, query, ids); err != nil {
			t.Fatal(err)
		}
	}

	slices.Reverse(ids)
	return ids
}

func TestSendBulk_ReadQueriesDoNotGrowWithRecipients(t *testing.T) {
	env := openTestEnv(t)
	userIDs := env.createBulkUsers(t, MaxBulkRecipients)
	counter := env.countSenderReads(t)

	for _, count := range []int{1, MaxBulkRecipients} {
		t.Run(fmt.Sprintf("recipients_%d", count), func(t *testing.T) {
			counter.reset()
			started := time.Now()
			results, err := env.sender.SendBulk(t.Context(), env.tenant.ID, "sales", orderConfirmed, userIDs[:count],
				map[string]any{"OrderReference": "ORD-BULK"}, Options{})
			if err != nil {
				t.Fatalf("SendBulk() error: %v", err)
			}

			queries := counter.reads()
			if len(queries) != 6 {
				t.Errorf("SendBulk(%d) read queries = %d, want 6: %v", count, len(queries), queries)
			}
			for _, table := range []string{"system.tenants", ".user_roles", "system.user_profiles", ".notification_preferences", ".user_device_tokens", ".notification_templates"} {
				found := 0
				for _, query := range queries {
					if strings.Contains(query, table) {
						found++
					}
				}
				if found != 1 {
					t.Errorf("reads of %s = %d, want 1", table, found)
				}
			}
			if len(results) != count {
				t.Fatalf("results = %d, want %d", len(results), count)
			}
			for i, res := range results {
				if res.UserID != userIDs[i] || !slices.Equal(res.ChannelsUsed, []string{inApp, email, push}) {
					t.Errorf("result[%d] = %+v, want user %s with in_app, email, push", i, res, userIDs[i])
				}
			}

			t.Logf("%d recipients with distinct locales: %d read queries, elapsed %s", count, len(queries), time.Since(started))
		})
	}
}

func TestSendBulk_RoutesEachUsersLoadedData(t *testing.T) {
	env := openTestEnv(t)
	ama := env.createUser(t, "Ama Owusu", "+233501111111")
	kofi := env.createUser(t, "Kofi Mensah", "")
	yaw := env.createUser(t, "Yaw Boateng", "")
	env.registerDevice(t, ama, "android", "ama-android")
	env.registerDevice(t, ama, "ios", "ama-ios")
	env.registerDevice(t, kofi, "web", "kofi-web")
	if err := env.store.UpdatePreferences(t.Context(), env.tenant.Slug, env.tenant.ID, ama,
		&notifications.ChannelsPatch{Email: new(false), Push: new(false)}, nil); err != nil {
		t.Fatal(err)
	}
	if err := env.store.UpdatePreferences(t.Context(), env.tenant.Slug, env.tenant.ID, kofi, nil,
		map[string]notifications.ChannelsPatch{orderConfirmed: {Email: new(false)}}); err != nil {
		t.Fatal(err)
	}

	results, err := env.sender.SendBulk(t.Context(), env.tenant.ID, "sales", orderConfirmed, []string{yaw, kofi, ama},
		map[string]any{"OrderReference": "ORD-ROUTES"}, Options{AdditionalChannels: []string{sms}})
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range [][]string{{inApp, email}, {inApp, push}, {inApp}} {
		if !slices.Equal(results[i].ChannelsUsed, want) {
			t.Errorf("result[%d].ChannelsUsed = %v, want %v", i, results[i].ChannelsUsed, want)
		}
	}
	for i, firstName := range []string{"Yaw", "Kofi", "Ama"} {
		if body := results[i].event.Body; body == nil || !strings.HasPrefix(*body, "Hi "+firstName+",") {
			t.Errorf("result[%d] body = %v, want greeting for %s", i, body, firstName)
		}
	}

	forced, err := env.sender.SendBulk(t.Context(), env.tenant.ID, "sales", orderConfirmed, []string{ama, kofi},
		map[string]any{"OrderReference": "ORD-FORCED"}, Options{ForceChannels: []string{sms, push}})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(forced[0].ChannelsUsed, []string{inApp, sms, push}) || !slices.Equal(forced[1].ChannelsUsed, []string{inApp, push}) {
		t.Errorf("forced channels = %v and %v", forced[0].ChannelsUsed, forced[1].ChannelsUsed)
	}

	for i, want := range [][]notifications.DeviceToken{
		{{Platform: "android", Token: "ama-android"}, {Platform: "ios", Token: "ama-ios"}},
		{{Platform: "web", Token: "kofi-web"}},
	} {
		var raw []byte
		if err := env.conn.QueryRowContext(t.Context(), `
			SELECT args FROM system.river_job
			WHERE args->>'tenant_id' = $1 AND args->>'notification_id' = $2 AND args->>'job_type' = 'push_send'
		`, env.tenant.ID, forced[i].NotificationID).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		var args jobqueue.WASMJobArgs
		if err := json.Unmarshal(raw, &args); err != nil {
			t.Fatal(err)
		}
		var payload abiv1.PushSendPayload
		if err := msgpack.Unmarshal(args.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		if len(payload.Tokens) != len(want) {
			t.Fatalf("user %s push tokens = %v, want %v", forced[i].UserID, payload.Tokens, want)
		}
		for j, token := range want {
			if got := payload.Tokens[j]; got.Platform != token.Platform || got.Token != token.Token {
				t.Errorf("user %s push token[%d] = %+v, want %+v", forced[i].UserID, j, got, token)
			}
		}
	}
}

func TestSendBulk_InvalidRecipientWritesNothing(t *testing.T) {
	for _, kind := range []string{"malformed", "missing", "suspended_member", "expired_role", "no_role", "deleted_user"} {
		t.Run(kind, func(t *testing.T) {
			env := openTestEnv(t)
			good := env.createUser(t, "Ama Owusu", "")
			bad := env.createUser(t, "Kofi Mensah", "")
			schema := tenantschema.Name(env.tenant.Slug)
			var query string
			switch kind {
			case "malformed":
				bad = "not-a-uuid"
			case "missing":
				bad = uuid.New().String()
			case "suspended_member":
				query = `UPDATE ` + schema + `.tenant_members SET status = 'suspended' WHERE user_id = $1`
			case "expired_role":
				query = `UPDATE ` + schema + `.user_roles SET expires_at = NOW() - INTERVAL '1 second' WHERE user_id = $1`
			case "no_role":
				query = `DELETE FROM ` + schema + `.user_roles WHERE user_id = $1`
			case "deleted_user":
				query = `UPDATE system.users SET deleted_at = NOW() WHERE id = $1`
			}
			if query != "" {
				if _, err := env.conn.ExecContext(t.Context(), query, bad); err != nil {
					t.Fatal(err)
				}
			}

			_, err := env.sender.SendBulk(t.Context(), env.tenant.ID, "sales", orderConfirmed, []string{good, bad}, nil, Options{})
			if !errors.Is(err, ErrUnknownUser) {
				t.Fatalf("SendBulk() error = %v, want ErrUnknownUser", err)
			}
			if n := env.notificationCount(t); n != 0 {
				t.Errorf("notification count = %d, want 0", n)
			}
			if env.jobCount(t) != 0 || len(env.hub.sent) != 0 {
				t.Error("rejected bulk send created jobs or announced notifications")
			}
		})
	}
}
