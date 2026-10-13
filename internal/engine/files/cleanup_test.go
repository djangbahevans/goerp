package files

import (
	"errors"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
)

func TestDeletedBefore_ExactCutoffAndPurgeGuard(t *testing.T) {
	store, conn, slug := openTestStore(t)
	cutoff := time.Now().UTC().Truncate(time.Second)
	old := insertFixtureFile(t, store, slug, "attachments")
	boundary := insertFixtureFile(t, store, slug, "attachments")
	active := insertFixtureFile(t, store, slug, "attachments")
	for id, deletedAt := range map[string]time.Time{old.ID: cutoff.Add(-time.Second), boundary.ID: cutoff} {
		if _, err := conn.ExecContext(t.Context(), "UPDATE "+tenantschema.Name(slug)+".files SET deleted_at = $1 WHERE id = $2", deletedAt, id); err != nil {
			t.Fatal(err)
		}
	}

	candidates, err := store.DeletedBefore(t.Context(), slug, cutoff, "", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 || candidates[0].ID != old.ID || candidates[0].StorageKey != old.StorageKey {
		t.Fatalf("candidates = %v, want old file %s", candidates, old.ID)
	}
	if next, err := store.DeletedBefore(t.Context(), slug, cutoff, old.ID, 1); err != nil || len(next) != 0 {
		t.Fatalf("next page = %v, error %v", next, err)
	}

	for _, row := range []InsertRow{boundary, active} {
		if err := store.PurgeDeleted(t.Context(), slug, DeletedFile{ID: row.ID, StorageKey: row.StorageKey}, cutoff); err != nil {
			t.Fatal(err)
		}
		if _, err := store.GetByID(t.Context(), slug, row.ID); err != nil {
			t.Errorf("protected file %s: %v", row.ID, err)
		}
	}
	if err := store.PurgeDeleted(t.Context(), slug, DeletedFile{ID: old.ID, StorageKey: "different-key"}, cutoff); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetByID(t.Context(), slug, old.ID); err != nil {
		t.Fatal("purge removed a file with a mismatched key")
	}
	if err := store.PurgeDeleted(t.Context(), slug, candidates[0], cutoff); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetByID(t.Context(), slug, old.ID); !errors.Is(err, ErrFileNotFound) {
		t.Fatalf("purged file error = %v", err)
	}
}

func TestDeletedBefore_MissingTable(t *testing.T) {
	store, conn, slug := openTestStore(t)
	if _, err := conn.ExecContext(t.Context(), "DROP TABLE "+tenantschema.Name(slug)+".files"); err != nil {
		t.Fatal(err)
	}
	if candidates, err := store.DeletedBefore(t.Context(), slug, time.Now(), "", 100); err != nil || len(candidates) != 0 {
		t.Fatalf("candidates = %v, error %v", candidates, err)
	}
}
