package savedfilters

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
)

// localPostgresDSN points directly at the compose.dev.yml Postgres
// instance, same convention as internal/engine/recordshares' tests.
const localPostgresDSN = "postgres://goerp:dev@localhost:55432/goerp"

// openTestStore creates a fixture tenant_<random> schema directly (this
// package's tests don't wait on real tenant provisioning to exist — same
// reasoning recordshares_test.go's own openTestStore already
// established) and returns a Store plus that schema's slug for tests to
// target.
func openTestStore(t *testing.T) (store *Store, slug string) {
	t.Helper()

	conn, err := db.New(localPostgresDSN)
	if err != nil {
		t.Skipf("postgres not reachable at %s (start compose.dev.yml): %v", localPostgresDSN, err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	slug = fmt.Sprintf("savedfilterstest%d", time.Now().UnixNano())
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

	return store, slug
}

func TestBootstrap_CreatesTableAndIndex(t *testing.T) {
	store, slug := openTestStore(t)

	filters, err := store.ListForUserAndView(t.Context(), slug, "00000000-0000-0000-0000-000000000001", "contacts_list")
	if err != nil {
		t.Fatalf("ListForUserAndView() error: %v", err)
	}
	if len(filters) != 0 {
		t.Errorf("filters = %v, want empty immediately after provisioning", filters)
	}
}

func TestBootstrap_IsIdempotent(t *testing.T) {
	store, slug := openTestStore(t)

	if err := store.Bootstrap(t.Context(), slug); err != nil {
		t.Fatalf("second Bootstrap() call error: %v", err)
	}
}

func TestCreate_ReturnsTheStoredRow(t *testing.T) {
	store, slug := openTestStore(t)

	sf, err := store.Create(t.Context(), slug, "00000000-0000-0000-0000-000000000001", "contacts_list", "Active only", "filter[is_active]=true", false)
	if err != nil {
		t.Fatalf("Create() error: %v", err)
	}
	if sf.ID == "" {
		t.Error("expected a generated id")
	}
	if sf.UserID != "00000000-0000-0000-0000-000000000001" || sf.ViewName != "contacts_list" || sf.Label != "Active only" ||
		sf.QueryString != "filter[is_active]=true" || sf.IsDefault {
		t.Errorf("Create() = %+v, unexpected field values", sf)
	}
}

func TestListForUserAndView_ScopesToOwnRowsAndView(t *testing.T) {
	store, slug := openTestStore(t)

	mine, err := store.Create(t.Context(), slug, "00000000-0000-0000-0000-000000000001", "contacts_list", "Mine", "filter[a]=1", false)
	if err != nil {
		t.Fatalf("Create() error: %v", err)
	}
	if _, err := store.Create(t.Context(), slug, "00000000-0000-0000-0000-000000000002", "contacts_list", "Someone else's", "filter[b]=2", false); err != nil {
		t.Fatalf("Create() error: %v", err)
	}
	if _, err := store.Create(t.Context(), slug, "00000000-0000-0000-0000-000000000001", "orders_list", "Different view", "filter[c]=3", false); err != nil {
		t.Fatalf("Create() error: %v", err)
	}

	filters, err := store.ListForUserAndView(t.Context(), slug, "00000000-0000-0000-0000-000000000001", "contacts_list")
	if err != nil {
		t.Fatalf("ListForUserAndView() error: %v", err)
	}
	if len(filters) != 1 || filters[0].ID != mine.ID {
		t.Errorf("ListForUserAndView() = %+v, want only %+v", filters, mine)
	}
}

func TestGet_ReturnsErrNotFoundForAMissingID(t *testing.T) {
	store, slug := openTestStore(t)

	if _, err := store.Get(t.Context(), slug, "00000000-0000-0000-0000-000000000000"); err != ErrNotFound {
		t.Errorf("Get() error = %v, want ErrNotFound", err)
	}
}

func TestUpdate_RenamesWithoutTouchingQueryStringOrDefault(t *testing.T) {
	store, slug := openTestStore(t)

	sf, err := store.Create(t.Context(), slug, "00000000-0000-0000-0000-000000000001", "contacts_list", "Original", "filter[a]=1", true)
	if err != nil {
		t.Fatalf("Create() error: %v", err)
	}

	newLabel := "Renamed"
	updated, err := store.Update(t.Context(), slug, sf.ID, &newLabel, nil)
	if err != nil {
		t.Fatalf("Update() error: %v", err)
	}
	if updated.Label != "Renamed" || updated.QueryString != "filter[a]=1" || !updated.IsDefault {
		t.Errorf("Update() = %+v, want label renamed and everything else unchanged", updated)
	}
}

func TestUpdate_SettingDefaultClearsAnyOtherDefaultOnTheSameUserAndView(t *testing.T) {
	store, slug := openTestStore(t)

	first, err := store.Create(t.Context(), slug, "00000000-0000-0000-0000-000000000001", "contacts_list", "First", "filter[a]=1", true)
	if err != nil {
		t.Fatalf("Create() error: %v", err)
	}
	second, err := store.Create(t.Context(), slug, "00000000-0000-0000-0000-000000000001", "contacts_list", "Second", "filter[b]=2", false)
	if err != nil {
		t.Fatalf("Create() error: %v", err)
	}

	isDefault := true
	if _, err := store.Update(t.Context(), slug, second.ID, nil, &isDefault); err != nil {
		t.Fatalf("Update() error: %v", err)
	}

	refreshedFirst, err := store.Get(t.Context(), slug, first.ID)
	if err != nil {
		t.Fatalf("Get() error: %v", err)
	}
	if refreshedFirst.IsDefault {
		t.Error("expected the first saved filter's is_default to be cleared once the second became default")
	}
}

func TestUpdate_SettingDefaultDoesNotAffectAnotherViewsDefault(t *testing.T) {
	store, slug := openTestStore(t)

	otherView, err := store.Create(t.Context(), slug, "00000000-0000-0000-0000-000000000001", "orders_list", "Other view default", "filter[a]=1", true)
	if err != nil {
		t.Fatalf("Create() error: %v", err)
	}
	target, err := store.Create(t.Context(), slug, "00000000-0000-0000-0000-000000000001", "contacts_list", "Target", "filter[b]=2", false)
	if err != nil {
		t.Fatalf("Create() error: %v", err)
	}

	isDefault := true
	if _, err := store.Update(t.Context(), slug, target.ID, nil, &isDefault); err != nil {
		t.Fatalf("Update() error: %v", err)
	}

	refreshedOtherView, err := store.Get(t.Context(), slug, otherView.ID)
	if err != nil {
		t.Fatalf("Get() error: %v", err)
	}
	if !refreshedOtherView.IsDefault {
		t.Error("expected a different view's default to be left untouched")
	}
}

func TestUpdate_ReturnsErrNotFoundForAMissingID(t *testing.T) {
	store, slug := openTestStore(t)

	newLabel := "Renamed"
	if _, err := store.Update(t.Context(), slug, "00000000-0000-0000-0000-000000000000", &newLabel, nil); err != ErrNotFound {
		t.Errorf("Update() error = %v, want ErrNotFound", err)
	}
}

func TestDelete_RemovesTheRow(t *testing.T) {
	store, slug := openTestStore(t)

	sf, err := store.Create(t.Context(), slug, "00000000-0000-0000-0000-000000000001", "contacts_list", "Temp", "filter[a]=1", false)
	if err != nil {
		t.Fatalf("Create() error: %v", err)
	}

	if err := store.Delete(t.Context(), slug, sf.ID); err != nil {
		t.Fatalf("Delete() error: %v", err)
	}
	if _, err := store.Get(t.Context(), slug, sf.ID); err != ErrNotFound {
		t.Errorf("Get() after Delete() error = %v, want ErrNotFound", err)
	}
}

func TestDelete_ReturnsErrNotFoundForAMissingID(t *testing.T) {
	store, slug := openTestStore(t)

	if err := store.Delete(t.Context(), slug, "00000000-0000-0000-0000-000000000000"); err != ErrNotFound {
		t.Errorf("Delete() error = %v, want ErrNotFound", err)
	}
}
