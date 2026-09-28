package notifications

import (
	"context"
	"fmt"
	"testing"
	"time"
	"uuid"

	"github.com/djangbahevans/goerp/internal/engine/cache"
	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
)

const (
	testPostgresDSN = "postgres://goerp:dev@localhost:55432/goerp"
	testRedisAddr   = "localhost:6379"
)

// newCachedPreferencesStore is a Store with a bootstrapped
// notification_preferences table in a fresh tenant schema and a Redis
// cache.
func newCachedPreferencesStore(t *testing.T) (*Store, string) {
	t.Helper()
	conn, err := db.New(testPostgresDSN)
	if err != nil {
		t.Skipf("postgres not reachable at %s (start compose.dev.yml): %v", testPostgresDSN, err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	c, err := cache.New(ctx, cache.Config{Addr: testRedisAddr, MaxRetries: 1})
	if err != nil {
		t.Skipf("redis not reachable at %s (start compose.dev.yml): %v", testRedisAddr, err)
	}
	t.Cleanup(func() { _ = c.Close() })

	slug := fmt.Sprintf("notifprefstest%d", time.Now().UnixNano())
	schema := tenantschema.Name(slug)
	if _, err := conn.ExecContext(t.Context(), "CREATE SCHEMA "+schema); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(func() { _, _ = conn.Exec("DROP SCHEMA " + schema + " CASCADE") })

	s := NewStore(conn).WithCache(c)
	if err := s.BootstrapPreferences(t.Context(), slug); err != nil {
		t.Fatalf("BootstrapPreferences() error: %v", err)
	}
	return s, slug
}

// TestPreferences_ARaceWithAWriteDoesNotCacheStalePreferences commits a
// write between a read's database load and its cache write: the read may
// return what it loaded, but must not cache it over the write.
func TestPreferences_ARaceWithAWriteDoesNotCacheStalePreferences(t *testing.T) {
	for _, warm := range []bool{false, true} {
		t.Run(fmt.Sprintf("warm=%v", warm), func(t *testing.T) {
			s, slug := newCachedPreferencesStore(t)
			ctx := t.Context()
			tenantID, userID := uuid.New().String(), uuid.New().String()

			if warm {
				// A previous write leaves a generation but no data.
				if err := s.UpdatePreferences(ctx, slug, tenantID, userID, &ChannelsPatch{SMS: new(false)}, nil); err != nil {
					t.Fatalf("UpdatePreferences() error: %v", err)
				}
			}

			s.afterLoad = func() {
				s.afterLoad = nil
				patch := map[string]ChannelsPatch{"sales.order_confirmed": {Email: new(false)}}
				if err := s.UpdatePreferences(ctx, slug, tenantID, userID, nil, patch); err != nil {
					t.Fatalf("UpdatePreferences() error: %v", err)
				}
			}
			if _, err := s.Preferences(ctx, slug, tenantID, userID); err != nil {
				t.Fatalf("racing Preferences() error: %v", err)
			}

			for range 2 {
				p, err := s.Preferences(ctx, slug, tenantID, userID)
				if err != nil {
					t.Fatalf("Preferences() error: %v", err)
				}
				if c, ok := p.Types["sales.order_confirmed"]; !ok || c.Email {
					t.Fatalf("types = %+v, want sales.order_confirmed with email off", p.Types)
				}
			}
		})
	}
}

func TestPreferences_ServesTheCacheUntilAWrite(t *testing.T) {
	s, slug := newCachedPreferencesStore(t)
	ctx := t.Context()
	tenantID, userID := uuid.New().String(), uuid.New().String()

	if _, err := s.Preferences(ctx, slug, tenantID, userID); err != nil {
		t.Fatalf("Preferences() error: %v", err)
	}
	// A write that bypasses the store isn't seen while the entry is cached.
	if _, err := s.db.ExecContext(ctx, fmt.Sprintf(
		`INSERT INTO %s.notification_preferences (tenant_id, user_id, push_enabled) VALUES ($1, $2, false)`,
		tenantschema.Name(slug)), tenantID, userID); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if p, _ := s.Preferences(ctx, slug, tenantID, userID); !p.Global.Push {
		t.Fatalf("global = %+v, want the cached push default", p.Global)
	}

	if err := s.UpdatePreferences(ctx, slug, tenantID, userID, &ChannelsPatch{Email: new(false)}, nil); err != nil {
		t.Fatalf("UpdatePreferences() error: %v", err)
	}
	p, err := s.Preferences(ctx, slug, tenantID, userID)
	if err != nil {
		t.Fatalf("Preferences() error: %v", err)
	}
	if p.Global != (Channels{Email: false, SMS: false, Push: false}) {
		t.Errorf("global = %+v, want email and push off", p.Global)
	}
}
