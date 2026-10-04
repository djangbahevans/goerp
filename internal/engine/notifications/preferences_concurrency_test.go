package notifications

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"testing"
	"time"
	"uuid"

	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
)

func newConcurrentPreferencesStore(t *testing.T) (*Store, string, string) {
	t.Helper()

	appName := "notifprefs-" + uuid.New().String()
	conn, err := db.New(testPostgresDSN + "?application_name=" + appName)
	if err != nil {
		t.Skipf("postgres not reachable at %s (start compose.dev.yml): %v", testPostgresDSN, err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	slug := fmt.Sprintf("notifconcurrent%d", time.Now().UnixNano())
	schema := tenantschema.Name(slug)
	if _, err := conn.ExecContext(t.Context(), "CREATE SCHEMA "+schema); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(func() { _, _ = conn.Exec("DROP SCHEMA " + schema + " CASCADE") })

	s := NewStore(conn)
	if err := s.BootstrapPreferences(t.Context(), slug); err != nil {
		t.Fatalf("BootstrapPreferences() error: %v", err)
	}

	return s, slug, appName
}

func waitForPreferenceWriters(t *testing.T, ctx context.Context, conn *sql.DB, appName string, want int) {
	t.Helper()

	for tick := time.Tick(10 * time.Millisecond); ; {
		var waiting int
		err := conn.QueryRowContext(ctx, `
			SELECT count(*) FROM pg_stat_activity
			WHERE application_name = $1 AND state = 'active' AND wait_event_type = 'Lock'
		`, appName).Scan(&waiting)
		if err != nil {
			t.Fatalf("observe blocked preference writers: %v", err)
		}
		if waiting == want {
			return
		}

		select {
		case <-ctx.Done():
			t.Fatalf("blocked preference writers = %d, want %d: %v", waiting, want, ctx.Err())
		case <-tick:
		}
	}
}

func TestUpdatePreferences_ConcurrentPatches(t *testing.T) {
	cases := []struct {
		name        string
		global      bool
		initialType *ChannelsPatch
		first       ChannelsPatch
		second      ChannelsPatch
		want        *Channels
	}{
		{
			name:   "missing_type",
			first:  ChannelsPatch{Email: new(false)},
			second: ChannelsPatch{SMS: new(false)},
			want:   new(Channels{Push: true}),
		},
		{
			name:        "existing_type",
			initialType: new(ChannelsPatch{Push: new(false)}),
			first:       ChannelsPatch{Email: new(false)},
			second:      ChannelsPatch{SMS: new(false)},
			want:        new(Channels{}),
		},
		{
			name:   "global",
			global: true,
			first:  ChannelsPatch{Email: new(false)},
			second: ChannelsPatch{SMS: new(false)},
			want:   new(Channels{Push: true}),
		},
		{
			name:        "reset_to_global",
			initialType: new(ChannelsPatch{Email: new(false), Push: new(false)}),
			first:       ChannelsPatch{Email: new(true)},
			second:      ChannelsPatch{Push: new(true)},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, slug, appName := newConcurrentPreferencesStore(t)
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			var wg sync.WaitGroup
			t.Cleanup(func() {
				cancel()
				wg.Wait()
			})
			tenantID, userID := uuid.New().String(), uuid.New().String()
			const typ = "sales.order_confirmed"

			if err := s.UpdatePreferences(ctx, slug, tenantID, userID, &ChannelsPatch{SMS: new(true)}, nil); err != nil {
				t.Fatalf("set global preferences: %v", err)
			}
			if tc.initialType != nil {
				if err := s.UpdatePreferences(ctx, slug, tenantID, userID, nil, map[string]ChannelsPatch{typ: *tc.initialType}); err != nil {
					t.Fatalf("set type preferences: %v", err)
				}
			}

			gate, err := s.db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatalf("begin write barrier: %v", err)
			}
			defer func() { _ = gate.Rollback() }()

			// SHARE permits preference reads but holds writes until both calls
			// are blocked. Without serialization, both read the same old values.
			if _, err := gate.ExecContext(ctx, "LOCK TABLE "+tenantschema.Name(slug)+".notification_preferences IN SHARE MODE"); err != nil {
				t.Fatalf("hold write barrier: %v", err)
			}

			results := make(chan error, 2)
			for _, patch := range []ChannelsPatch{tc.first, tc.second} {
				wg.Go(func() {
					if tc.global {
						results <- s.UpdatePreferences(ctx, slug, tenantID, userID, &patch, nil)
					} else {
						results <- s.UpdatePreferences(ctx, slug, tenantID, userID, nil, map[string]ChannelsPatch{typ: patch})
					}
				})
			}
			waitForPreferenceWriters(t, ctx, s.db, appName, 2)

			if err := gate.Commit(); err != nil {
				t.Fatalf("release write barrier: %v", err)
			}
			wg.Wait()
			for range 2 {
				if err := <-results; err != nil {
					t.Fatalf("concurrent UpdatePreferences() error: %v", err)
				}
			}

			p, err := s.Preferences(ctx, slug, tenantID, userID)
			if err != nil {
				t.Fatalf("Preferences() error: %v", err)
			}
			if tc.global {
				if p.Global != *tc.want {
					t.Errorf("global = %+v, want %+v", p.Global, *tc.want)
				}
			} else if got, exists := p.Types[typ]; tc.want == nil {
				if exists {
					t.Errorf("type = %+v, want no override", got)
				}
			} else if !exists || got != *tc.want {
				t.Errorf("type = %+v (exists=%v), want %+v", got, exists, *tc.want)
			}
		})
	}
}

func TestUpdatePreferences_IndependentUsersAndTenants(t *testing.T) {
	for _, scope := range []string{"user", "tenant"} {
		t.Run(scope, func(t *testing.T) {
			s, slug, appName := newConcurrentPreferencesStore(t)
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			var wg sync.WaitGroup
			t.Cleanup(func() {
				cancel()
				wg.Wait()
			})
			tenantID, userID := uuid.New().String(), uuid.New().String()

			if err := s.UpdatePreferences(ctx, slug, tenantID, userID, &ChannelsPatch{SMS: new(true)}, nil); err != nil {
				t.Fatalf("set global preferences: %v", err)
			}

			gate, err := s.db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatalf("begin row barrier: %v", err)
			}
			defer func() { _ = gate.Rollback() }()

			if _, err := gate.ExecContext(ctx, fmt.Sprintf(`
				SELECT id FROM %s.notification_preferences
				WHERE tenant_id = $1 AND user_id = $2 FOR UPDATE
			`, tenantschema.Name(slug)), tenantID, userID); err != nil {
				t.Fatalf("hold row barrier: %v", err)
			}

			result := make(chan error, 1)
			wg.Go(func() {
				result <- s.UpdatePreferences(ctx, slug, tenantID, userID, &ChannelsPatch{Email: new(false)}, nil)
			})
			waitForPreferenceWriters(t, ctx, s.db, appName, 1)

			otherTenant, otherUser := tenantID, userID
			if scope == "user" {
				otherUser = uuid.New().String()
			} else {
				otherTenant = uuid.New().String()
			}
			if err := s.UpdatePreferences(ctx, slug, otherTenant, otherUser, &ChannelsPatch{Email: new(false)}, nil); err != nil {
				t.Fatalf("update independent %s while first writer is blocked: %v", scope, err)
			}

			if err := gate.Commit(); err != nil {
				t.Fatalf("release row barrier: %v", err)
			}
			wg.Wait()
			if err := <-result; err != nil {
				t.Fatalf("blocked UpdatePreferences() error: %v", err)
			}

			p, err := s.Preferences(ctx, slug, otherTenant, otherUser)
			if err != nil {
				t.Fatalf("Preferences() error: %v", err)
			}
			if p.Global.Email {
				t.Error("independent preference update was not saved")
			}
		})
	}
}
