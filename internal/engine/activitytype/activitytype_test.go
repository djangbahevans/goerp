package activitytype

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"maps"
	"slices"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/internal/engine/recordactivity"
	"github.com/djangbahevans/goerp/internal/engine/scheduledactivity"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
)

// localPostgresDSN points directly at the compose.dev.yml Postgres
// instance, same convention as internal/engine/scheduledactivity's tests.
const localPostgresDSN = "postgres://goerp:dev@localhost:15432/goerp"

// openTestStore creates a fixture tenant schema with activity_types,
// seeded for locales, and the scheduled_activities table that references
// it.
func openTestStore(t *testing.T, locales []string) (store *Store, conn *sql.DB, slug string) {
	t.Helper()

	conn, err := db.New(localPostgresDSN)
	if err != nil {
		t.Skipf("postgres not reachable at %s (start compose.dev.yml): %v", localPostgresDSN, err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	slug = fmt.Sprintf("activitytypetest%d", time.Now().UnixNano())
	schema := tenantschema.Name(slug)
	if _, err := conn.ExecContext(t.Context(), "CREATE SCHEMA "+schema); err != nil {
		t.Fatalf("create fixture schema: %v", err)
	}
	t.Cleanup(func() {
		_, _ = conn.ExecContext(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
	})

	store = NewStore(conn)
	if err := store.Bootstrap(t.Context(), slug); err != nil {
		t.Fatalf("Bootstrap() error: %v", err)
	}
	if err := store.Seed(t.Context(), slug, locales); err != nil {
		t.Fatalf("Seed() error: %v", err)
	}
	if err := recordactivity.NewStore(conn).Bootstrap(t.Context(), slug); err != nil {
		t.Fatalf("recordactivity Bootstrap() error: %v", err)
	}
	if err := scheduledactivity.NewStore(conn).Bootstrap(t.Context(), slug); err != nil {
		t.Fatalf("scheduledactivity Bootstrap() error: %v", err)
	}
	return store, conn, slug
}

func schedule(t *testing.T, conn *sql.DB, slug, typ string) {
	t.Helper()
	_, err := scheduledactivity.NewStore(conn).Create(t.Context(), slug, scheduledactivity.NewActivity{
		Model: "sales.order", RecordID: "11111111-1111-1111-1111-111111111111", Type: typ, Summary: "Follow up",
		DueDate: "2026-10-01", AssigneeID: "00000000-0000-0000-0000-0000000000aa", CreatedBy: "00000000-0000-0000-0000-0000000000aa",
	})
	if err != nil {
		t.Fatalf("schedule %s activity: %v", typ, err)
	}
}

func keys(types []Type) []string {
	out := make([]string, len(types))
	for i, t := range types {
		out[i] = t.Key
	}
	return out
}

func newType(key string) NewType {
	return NewType{Key: key, Label: map[string]string{"en": key}, Icon: "map-pin"}
}

func TestSeed_InsertsTheBuiltInTypesLabelledForEveryLocale(t *testing.T) {
	store, _, slug := openTestStore(t, []string{"fr", "ar", "fr-GH", "es"})

	types, err := store.List(t.Context(), slug, false)
	if err != nil {
		t.Fatalf("List() error: %v", err)
	}
	if got, want := keys(types), []string{"call", "meeting", "email", "todo"}; !slices.Equal(got, want) {
		t.Fatalf("keys = %v, want %v", got, want)
	}
	todo := types[3]
	want := map[string]string{"en": "To-do", "fr": "À faire", "ar": "مهمة", "fr-GH": "À faire", "es": "To-do"}
	if !maps.Equal(todo.Label, want) {
		t.Errorf("todo label = %v, want %v", todo.Label, want)
	}
	if todo.Icon != "square-check" || len(todo.DefaultSummary) != 0 || todo.DefaultDueDays != nil || todo.Archived() {
		t.Errorf("todo = %+v, want icon square-check, no defaults, active", todo)
	}
}

func TestSeed_LeavesATenantWithTypesAlone(t *testing.T) {
	store, _, slug := openTestStore(t, nil)
	ctx := t.Context()

	if err := store.Delete(ctx, slug, "email"); err != nil {
		t.Fatalf("Delete() error: %v", err)
	}
	if err := store.Seed(ctx, slug, nil); err != nil {
		t.Fatalf("second Seed() error: %v", err)
	}
	types, _ := store.List(ctx, slug, false)
	if got, want := keys(types), []string{"call", "meeting", "todo"}; !slices.Equal(got, want) {
		t.Errorf("keys after reseed = %v, want %v", got, want)
	}
}

func TestResolve_FollowsTheFallbackChain(t *testing.T) {
	m := map[string]string{"en": "Call", "fr": "Appel", "fr-CA": "Appel (CA)", "de": "Anruf"}
	cases := []struct {
		m                     map[string]string
		locale, tenantDefault string
		want                  string
	}{
		{m, "fr-CA", "en", "Appel (CA)"},
		{m, "fr-GH", "en", "Appel"},
		{m, "ar", "en", "Call"},
		{map[string]string{"fr": "Appel", "de": "Anruf"}, "ar", "fr", "Appel"},
		{map[string]string{"fr": "Appel", "de": "Anruf"}, "ar", "es", "Anruf"},
	}
	for _, c := range cases {
		if got, ok := Resolve(c.m, c.locale, c.tenantDefault); !ok || got != c.want {
			t.Errorf("Resolve(%v, %q, %q) = %q, %v; want %q", c.m, c.locale, c.tenantDefault, got, ok, c.want)
		}
	}
	if _, ok := Resolve(map[string]string{}, "en", "en"); ok {
		t.Error("Resolve(empty) ok = true, want false")
	}
}

func TestCreate_AddsLastAndRejectsATakenKeyOrTheLimit(t *testing.T) {
	store, _, slug := openTestStore(t, nil)
	ctx := t.Context()

	created, err := store.Create(ctx, slug, NewType{
		Key: "site_visit", Label: map[string]string{"en": "Site visit"}, Icon: "map-pin",
		DefaultSummary: map[string]string{"en": "Visit the site"}, DefaultDueDays: new(2),
	})
	if err != nil {
		t.Fatalf("Create() error: %v", err)
	}
	if created.Position != 5 || created.DefaultSummary["en"] != "Visit the site" || *created.DefaultDueDays != 2 {
		t.Errorf("created = %+v, want position 5 with its defaults", created)
	}

	if _, err := store.Update(ctx, slug, "site_visit", Update{Archived: new(true)}); err != nil {
		t.Fatalf("archive: %v", err)
	}
	if _, err := store.Create(ctx, slug, newType("site_visit")); !errors.Is(err, ErrKeyTaken) {
		t.Errorf("Create(archived key) err = %v, want ErrKeyTaken", err)
	}

	for i := 6; i <= MaxTypes; i++ {
		if _, err := store.Create(ctx, slug, newType(fmt.Sprintf("type_%d", i))); err != nil {
			t.Fatalf("Create(type_%d) error: %v", i, err)
		}
	}
	if _, err := store.Create(ctx, slug, newType("one_too_many")); !errors.Is(err, ErrLimitReached) {
		t.Errorf("Create(51st) err = %v, want ErrLimitReached", err)
	}
}

func TestUpdate_ReplacesMapsAndCannotArchiveTheLastActiveType(t *testing.T) {
	store, _, slug := openTestStore(t, []string{"fr"})
	ctx := t.Context()

	updated, err := store.Update(ctx, slug, "call", Update{
		Label: map[string]string{"en": "Phone call"}, Icon: new("phone-call"),
		DefaultSummary: map[string]string{"en": "Call them"}, DefaultDueDays: new(1),
	})
	if err != nil {
		t.Fatalf("Update() error: %v", err)
	}
	if !maps.Equal(updated.Label, map[string]string{"en": "Phone call"}) || updated.Icon != "phone-call" || *updated.DefaultDueDays != 1 {
		t.Errorf("updated = %+v", updated)
	}
	cleared, err := store.Update(ctx, slug, "call", Update{DefaultSummary: map[string]string{}, ClearDefaultDueDays: true})
	if err != nil {
		t.Fatalf("clear defaults: %v", err)
	}
	if len(cleared.DefaultSummary) != 0 || cleared.DefaultDueDays != nil || cleared.Label["en"] != "Phone call" {
		t.Errorf("cleared = %+v, want no defaults and the label kept", cleared)
	}

	for _, key := range []string{"call", "meeting", "email"} {
		if _, err := store.Update(ctx, slug, key, Update{Archived: new(true)}); err != nil {
			t.Fatalf("archive %s: %v", key, err)
		}
	}
	if _, err := store.Update(ctx, slug, "todo", Update{Archived: new(true)}); !errors.Is(err, ErrLastActive) {
		t.Errorf("archive last active err = %v, want ErrLastActive", err)
	}
	// Re-archiving an archived type and restoring one are both fine.
	if _, err := store.Update(ctx, slug, "call", Update{Archived: new(true)}); err != nil {
		t.Errorf("re-archive: %v", err)
	}
	restored, err := store.Update(ctx, slug, "call", Update{Archived: new(false)})
	if err != nil || restored.Archived() {
		t.Errorf("restore = %+v, %v; want active", restored, err)
	}
	if _, err := store.Update(ctx, slug, "nope", Update{}); !errors.Is(err, ErrNotFound) {
		t.Errorf("Update(unknown) err = %v, want ErrNotFound", err)
	}
}

func TestReorder_NeedsEveryKeyExactlyOnce(t *testing.T) {
	store, _, slug := openTestStore(t, nil)
	ctx := t.Context()

	for _, bad := range [][]string{
		{"todo", "email", "meeting"},
		{"todo", "email", "meeting", "call", "call"},
		{"todo", "email", "meeting", "visit"},
		{},
	} {
		if err := store.Reorder(ctx, slug, bad); !errors.Is(err, ErrOrderMismatch) {
			t.Errorf("Reorder(%v) err = %v, want ErrOrderMismatch", bad, err)
		}
	}
	want := []string{"todo", "email", "meeting", "call"}
	if err := store.Reorder(ctx, slug, want); err != nil {
		t.Fatalf("Reorder() error: %v", err)
	}
	types, _ := store.List(ctx, slug, false)
	if got := keys(types); !slices.Equal(got, want) {
		t.Errorf("order = %v, want %v", got, want)
	}
}

func TestDelete_OnlyAnUnusedTypeAndNeverTheLastActive(t *testing.T) {
	store, conn, slug := openTestStore(t, nil)
	ctx := t.Context()

	schedule(t, conn, slug, "call")
	types, err := store.List(ctx, slug, true)
	if err != nil {
		t.Fatalf("List(usage) error: %v", err)
	}
	if types[0].UsageCount != 1 || types[1].UsageCount != 0 {
		t.Errorf("usage = %d, %d; want 1, 0", types[0].UsageCount, types[1].UsageCount)
	}
	if err := store.Delete(ctx, slug, "call"); !errors.Is(err, ErrInUse) {
		t.Errorf("Delete(used) err = %v, want ErrInUse", err)
	}
	if err := store.Delete(ctx, slug, "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Delete(unknown) err = %v, want ErrNotFound", err)
	}

	for _, key := range []string{"meeting", "email"} {
		if err := store.Delete(ctx, slug, key); err != nil {
			t.Fatalf("Delete(%s) error: %v", key, err)
		}
	}
	if _, err := store.Update(ctx, slug, "call", Update{Archived: new(true)}); err != nil {
		t.Fatalf("archive call: %v", err)
	}
	if err := store.Delete(ctx, slug, "todo"); !errors.Is(err, ErrLastActive) {
		t.Errorf("Delete(last active) err = %v, want ErrLastActive", err)
	}
}

func TestForeignKey_RefusesDeletingAUsedTypeAndAnUnknownType(t *testing.T) {
	_, conn, slug := openTestStore(t, nil)
	schedule(t, conn, slug, "meeting")

	_, err := conn.ExecContext(t.Context(), "DELETE FROM "+tenantschema.Name(slug)+".activity_types WHERE key = 'meeting'")
	if err == nil {
		t.Error("deleting a used type succeeded, want a foreign key violation")
	}
	_, err = scheduledactivity.NewStore(conn).Create(t.Context(), slug, scheduledactivity.NewActivity{
		Model: "sales.order", RecordID: "11111111-1111-1111-1111-111111111111", Type: "visit", Summary: "Follow up",
		DueDate: "2026-10-01", AssigneeID: "00000000-0000-0000-0000-0000000000aa", CreatedBy: "00000000-0000-0000-0000-0000000000aa",
	})
	if !errors.Is(err, scheduledactivity.ErrUnknownType) {
		t.Errorf("Create(unknown type) err = %v, want ErrUnknownType", err)
	}
}

func TestIsActive(t *testing.T) {
	store, _, slug := openTestStore(t, nil)
	ctx := t.Context()
	if _, err := store.Update(ctx, slug, "email", Update{Archived: new(true)}); err != nil {
		t.Fatalf("archive: %v", err)
	}
	for key, want := range map[string]bool{"call": true, "email": false, "visit": false} {
		if got, err := store.IsActive(ctx, slug, key); err != nil || got != want {
			t.Errorf("IsActive(%s) = %v, %v; want %v", key, got, err, want)
		}
	}
}

func TestValidIcon(t *testing.T) {
	for name, want := range map[string]bool{"phone": true, "square-check": true, "map-pin": true, "not-an-icon": false, "": false} {
		if got := ValidIcon(name); got != want {
			t.Errorf("ValidIcon(%q) = %v, want %v", name, got, want)
		}
	}
}
