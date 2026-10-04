package notifications

import (
	"context"
	"fmt"
	"maps"
	"strings"
	"testing"
	"uuid"

	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
)

func TestPreferencesForUsers_ReadsDatabaseWithoutChangingCache(t *testing.T) {
	s, slug := newCachedPreferencesStore(t)
	ctx := t.Context()
	tenantID, warmUser, coldUser := uuid.New().String(), uuid.New().String(), uuid.New().String()
	warmKey, coldKey := preferencesCacheKey(tenantID, warmUser), preferencesCacheKey(tenantID, coldUser)
	t.Cleanup(func() {
		_ = s.cache.Delete(context.Background(), warmKey)
		_ = s.cache.Delete(context.Background(), coldKey)
	})

	if err := s.UpdatePreferences(ctx, slug, tenantID, warmUser, &ChannelsPatch{SMS: new(true)},
		map[string]ChannelsPatch{"sales.order_confirmed": {Email: new(false)}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Preferences(ctx, slug, tenantID, warmUser); err != nil {
		t.Fatal(err)
	}
	before, found, err := s.cache.GetHash(ctx, warmKey)
	if err != nil || !found {
		t.Fatalf("read warmed cache: found=%v, error=%v", found, err)
	}

	if _, err := s.db.ExecContext(ctx, fmt.Sprintf(`
		UPDATE %s.notification_preferences SET push_enabled = false
		WHERE tenant_id = $1 AND user_id = $2 AND notification_type IS NULL
	`, tenantschema.Name(slug)), tenantID, warmUser); err != nil {
		t.Fatal(err)
	}

	prefs, err := s.PreferencesForUsers(ctx, slug, tenantID, []string{strings.ToUpper(warmUser), coldUser})
	if err != nil {
		t.Fatal(err)
	}
	if got := prefs[warmUser]; got == nil || got.Global != (Channels{Email: true, SMS: true, Push: false}) ||
		got.Types["sales.order_confirmed"] != (Channels{Email: false, SMS: true, Push: true}) {
		t.Errorf("warm user's bulk preferences = %+v, want database global settings and type override", got)
	}
	if got := prefs[coldUser]; got == nil || got.Global != DefaultChannels || len(got.Types) != 0 {
		t.Errorf("cold user's bulk preferences = %+v, want defaults", got)
	}

	after, found, err := s.cache.GetHash(ctx, warmKey)
	if err != nil || !found || !maps.Equal(before, after) {
		t.Errorf("warmed cache changed: before=%v, after=%v, found=%v, error=%v", before, after, found, err)
	}
	if _, found, err := s.cache.GetHash(ctx, coldKey); err != nil || found {
		t.Errorf("bulk read populated cold cache: found=%v, error=%v", found, err)
	}
}
