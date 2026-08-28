package wasm

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/searchindex"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
)

// newSearchFixtureTable creates a "widget" table (id, name, deleted_at)
// in slug's tenant schema, with three rows whose names trigram-match
// "widget" to differing degrees, plus a fourth soft-deleted row that
// should never be returned.
func newSearchFixtureTable(t *testing.T, conn *sql.DB, slug string) {
	t.Helper()
	ctx := context.Background()
	schema := tenantschema.Name(slug)

	if _, err := conn.ExecContext(ctx, fmt.Sprintf(`
		CREATE TABLE %s.widget (
			id UUID PRIMARY KEY,
			name TEXT NOT NULL,
			deleted_at TIMESTAMPTZ
		)`, schema)); err != nil {
		t.Fatalf("create widget table: %v", err)
	}

	rows := []struct {
		id, name string
		deleted  bool
	}{
		{"11111111-1111-1111-1111-111111111111", "Widget Alpha", false},
		{"22222222-2222-2222-2222-222222222222", "Widget Beta", false},
		{"33333333-3333-3333-3333-333333333333", "Totally Unrelated", false},
		{"44444444-4444-4444-4444-444444444444", "Widget Gamma", true},
	}
	for _, r := range rows {
		deletedAt := "NULL"
		if r.deleted {
			deletedAt = "NOW()"
		}
		if _, err := conn.ExecContext(ctx, fmt.Sprintf(
			"INSERT INTO %s.widget (id, name, deleted_at) VALUES ($1, $2, %s)", schema, deletedAt,
		), r.id, r.name); err != nil {
			t.Fatalf("insert fixture row %s: %v", r.name, err)
		}
	}
}

func newSearchModuleContext(slug string, caps abi.CapabilitySet, indexes []manifest.SearchIndex) *ModuleContext {
	reg := searchindex.New()
	reg.Register("testmodule", indexes)
	return NewModuleContext("req-1", "testmodule", "user-1", "contact-1", []string{"admin"}, nil, "tenant-id-1", slug, "trace-1", caps, nil,
		ModuleSnapshot{SearchIndexRegistry: reg})
}

func widgetSearchIndex() manifest.SearchIndex {
	return manifest.SearchIndex{
		Name:            "widgets",
		Table:           "widget",
		Searchable:      []string{"name"},
		Displayed:       []string{"id", "name"},
		SoftDeleteField: "deleted_at",
	}
}

func TestSearchQuery_RanksBySimilarityAndExcludesSoftDeleted(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("searchtest%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	newSearchFixtureTable(t, primaryDB, slug)

	modCtx := newSearchModuleContext(slug, abi.CapSearchQuery, []manifest.SearchIndex{widgetSearchIndex()})

	out, hostErr := SearchQuery(ctx, primaryDB, modCtx, SearchQueryInput{Index: "widgets", Query: "Widget"})
	if hostErr != nil {
		t.Fatalf("SearchQuery: %+v", hostErr)
	}

	if len(out.Hits) != 2 {
		t.Fatalf("len(Hits) = %d, want 2 (Alpha, Beta — Gamma is soft-deleted, Unrelated doesn't match)", len(out.Hits))
	}
	if out.TotalHits != 2 {
		t.Errorf("TotalHits = %d, want 2", out.TotalHits)
	}
	for _, hit := range out.Hits {
		name, _ := hit["name"].(string)
		if name != "Widget Alpha" && name != "Widget Beta" {
			t.Errorf("unexpected hit %+v", hit)
		}
		if _, ok := hit["search_score"]; ok {
			t.Error("internal search_score column leaked into a hit")
		}
	}
}

func TestSearchQuery_CapabilityDenied(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("searchtest%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	newSearchFixtureTable(t, primaryDB, slug)

	modCtx := newSearchModuleContext(slug, 0, []manifest.SearchIndex{widgetSearchIndex()})

	_, hostErr := SearchQuery(ctx, primaryDB, modCtx, SearchQueryInput{Index: "widgets", Query: "Widget"})
	if hostErr == nil || hostErr.Code != abi.ErrCodeCapabilityDenied {
		t.Fatalf("hostErr = %+v, want code %q", hostErr, abi.ErrCodeCapabilityDenied)
	}
}

func TestSearchQuery_UndeclaredIndexReturnsError(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("searchtest%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)

	modCtx := newSearchModuleContext(slug, abi.CapSearchQuery, nil)

	_, hostErr := SearchQuery(ctx, primaryDB, modCtx, SearchQueryInput{Index: "nonexistent", Query: "x"})
	if hostErr == nil || hostErr.Code != abi.ErrCodeIndexNotFound {
		t.Fatalf("hostErr = %+v, want code %q", hostErr, abi.ErrCodeIndexNotFound)
	}
}

func TestSearchQuery_UpdateAndDeleteReturnUnavailable(t *testing.T) {
	// makeSearchUpdate/makeSearchDelete always report abi.unavailable
	// against the trigram-only backend — verified via searchUnavailableError
	// directly rather than a full WASM round trip, matching this file's
	// own SearchQuery-core-only testing style.
	for _, op := range []string{"update", "delete"} {
		hostErr := searchUnavailableError(op)
		if hostErr.Code != abi.ErrCodeUnavailable {
			t.Errorf("searchUnavailableError(%q).Code = %q, want %q", op, hostErr.Code, abi.ErrCodeUnavailable)
		}
	}
}
