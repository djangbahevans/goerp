package recordactivity

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
)

// localPostgresDSN points directly at the compose.dev.yml Postgres
// instance, same convention as internal/engine/recordshares' tests.
const localPostgresDSN = "postgres://goerp:dev@localhost:55432/goerp"

const (
	testModel    = "sales.order"
	testRecordID = "11111111-1111-1111-1111-111111111111"
	testAuthorID = "00000000-0000-0000-0000-0000000000aa"
)

func openTestStore(t *testing.T) (store *Store, conn *sql.DB, slug string) {
	t.Helper()

	conn, err := db.New(localPostgresDSN)
	if err != nil {
		t.Skipf("postgres not reachable at %s (start compose.dev.yml): %v", localPostgresDSN, err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	slug = fmt.Sprintf("recordactivitytest%d", time.Now().UnixNano())
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
	return store, conn, slug
}

func TestBootstrap_IsIdempotent(t *testing.T) {
	store, _, slug := openTestStore(t)

	if err := store.Bootstrap(t.Context(), slug); err != nil {
		t.Fatalf("second Bootstrap() call error: %v", err)
	}
}

func TestBootstrap_CheckConstraintsRejectInconsistentRows(t *testing.T) {
	_, conn, slug := openTestStore(t)
	schema := tenantschema.Name(slug)

	cases := map[string]string{
		"unknown kind":             `INSERT INTO %s.record_activity (model, record_id, kind) VALUES ('m', gen_random_uuid(), 'edited')`,
		"change without changes":   `INSERT INTO %s.record_activity (model, record_id, kind) VALUES ('m', gen_random_uuid(), 'change')`,
		"changes on a comment":     `INSERT INTO %s.record_activity (model, record_id, kind, body, changes) VALUES ('m', gen_random_uuid(), 'comment', 'x', '[]')`,
		"activity_done without it": `INSERT INTO %s.record_activity (model, record_id, kind) VALUES ('m', gen_random_uuid(), 'activity_done')`,
		"activity on a created":    `INSERT INTO %s.record_activity (model, record_id, kind, activity) VALUES ('m', gen_random_uuid(), 'created', '{}')`,
		"deleted non-comment":      `INSERT INTO %s.record_activity (model, record_id, kind, deleted_at) VALUES ('m', gen_random_uuid(), 'created', NOW())`,
	}
	for name, stmt := range cases {
		if _, err := conn.ExecContext(t.Context(), fmt.Sprintf(stmt, schema)); err == nil {
			t.Errorf("%s: insert succeeded, want a CHECK violation", name)
		}
	}
}

func TestCreateComment_ReturnsTheStoredRow(t *testing.T) {
	store, _, slug := openTestStore(t)

	e, err := store.CreateComment(t.Context(), slug, testModel, testRecordID, testAuthorID, "Move delivery to Friday.", "req-1", "")
	if err != nil {
		t.Fatalf("CreateComment() error: %v", err)
	}
	if e.ID == "" || e.Kind != KindComment || e.Model != testModel || e.RecordID != testRecordID {
		t.Errorf("CreateComment() = %+v, unexpected identity fields", e)
	}
	if e.Body == nil || *e.Body != "Move delivery to Friday." {
		t.Errorf("Body = %v, want the posted text", e.Body)
	}
	if e.AuthorID == nil || *e.AuthorID != testAuthorID {
		t.Errorf("AuthorID = %v, want %q", e.AuthorID, testAuthorID)
	}
	if e.DeletedAt != nil || len(e.Changes) != 0 || len(e.Activity) != 0 {
		t.Errorf("CreateComment() = %+v, want no deleted_at/changes/activity", e)
	}
}

func TestList_PagesNewestFirstReturningEveryEntryOnce(t *testing.T) {
	store, _, slug := openTestStore(t)

	const total = 7
	created := make([]string, total)
	for i := range total {
		e, err := store.CreateComment(t.Context(), slug, testModel, testRecordID, testAuthorID, fmt.Sprintf("comment %d", i), "", "")
		if err != nil {
			t.Fatalf("CreateComment() error: %v", err)
		}
		created[i] = e.ID
	}
	if _, err := store.CreateComment(t.Context(), slug, testModel, "22222222-2222-2222-2222-222222222222", testAuthorID, "other record", "", ""); err != nil {
		t.Fatalf("CreateComment() error: %v", err)
	}

	var got []string
	cursor := ""
	pages := 0
	for {
		entries, hasMore, err := store.List(t.Context(), slug, testModel, testRecordID, cursor, 3)
		if err != nil {
			t.Fatalf("List() error: %v", err)
		}
		pages++
		for _, e := range entries {
			got = append(got, e.ID)
		}
		if !hasMore {
			break
		}
		cursor = entries[len(entries)-1].ID
	}

	if pages != 3 {
		t.Errorf("pages = %d, want 3 for %d entries at limit 3", pages, total)
	}
	if len(got) != total {
		t.Fatalf("got %d entries, want %d: %v", len(got), total, got)
	}
	for i, id := range got {
		if want := created[total-1-i]; id != want {
			t.Errorf("entry %d = %s, want %s (newest first)", i, id, want)
		}
	}
}

func TestList_ExactlyFullPageReportsNoMore(t *testing.T) {
	store, _, slug := openTestStore(t)

	for i := range 2 {
		if _, err := store.CreateComment(t.Context(), slug, testModel, testRecordID, testAuthorID, fmt.Sprintf("c%d", i), "", ""); err != nil {
			t.Fatalf("CreateComment() error: %v", err)
		}
	}
	entries, hasMore, err := store.List(t.Context(), slug, testModel, testRecordID, "", 2)
	if err != nil {
		t.Fatalf("List() error: %v", err)
	}
	if len(entries) != 2 || hasMore {
		t.Errorf("List() = %d entries, hasMore %v; want 2, false", len(entries), hasMore)
	}
}

func TestDeleteComment_ClearsBodyAndIsIdempotent(t *testing.T) {
	store, _, slug := openTestStore(t)

	e, err := store.CreateComment(t.Context(), slug, testModel, testRecordID, testAuthorID, "oops", "", "")
	if err != nil {
		t.Fatalf("CreateComment() error: %v", err)
	}
	if err := store.DeleteComment(t.Context(), slug, e.ID); err != nil {
		t.Fatalf("DeleteComment() error: %v", err)
	}
	first, err := store.Get(t.Context(), slug, e.ID)
	if err != nil {
		t.Fatalf("Get() error: %v", err)
	}
	if first.Body != nil || first.DeletedAt == nil {
		t.Errorf("after delete: body = %v, deleted_at = %v; want nil body and a deleted_at", first.Body, first.DeletedAt)
	}

	if err := store.DeleteComment(t.Context(), slug, e.ID); err != nil {
		t.Fatalf("second DeleteComment() error: %v", err)
	}
	second, err := store.Get(t.Context(), slug, e.ID)
	if err != nil {
		t.Fatalf("Get() error: %v", err)
	}
	if !second.DeletedAt.Equal(*first.DeletedAt) {
		t.Errorf("deleted_at changed on a repeat delete: %v → %v", first.DeletedAt, second.DeletedAt)
	}
}

func TestDeleteComment_RejectsNonCommentEntries(t *testing.T) {
	store, conn, slug := openTestStore(t)

	var id string
	if err := conn.QueryRowContext(t.Context(), fmt.Sprintf(
		`INSERT INTO %s.record_activity (model, record_id, kind) VALUES ($1, $2, 'created') RETURNING id`, tenantschema.Name(slug),
	), testModel, testRecordID).Scan(&id); err != nil {
		t.Fatalf("seed created entry: %v", err)
	}

	if err := store.DeleteComment(t.Context(), slug, id); !errors.Is(err, ErrNotFound) {
		t.Errorf("DeleteComment() error = %v, want ErrNotFound", err)
	}
}

func TestGet_ReturnsErrNotFoundForAMissingID(t *testing.T) {
	store, _, slug := openTestStore(t)

	if _, err := store.Get(t.Context(), slug, "00000000-0000-0000-0000-000000000000"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get() error = %v, want ErrNotFound", err)
	}
}
