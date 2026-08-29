package schema

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/sdk/go/model"
)

func TestCheckDowngrade_NewVersionNotLowerReturnsNone(t *testing.T) {
	sess, engine := setupTenantSchema(t, "downgrade_notlower")

	cases := map[string]struct{ current, next string }{
		"equal version":  {"1.5.0", "1.5.0"},
		"higher version": {"1.5.0", "2.0.0"},
	}

	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			status, blocked, err := engine.CheckDowngrade(context.Background(), sess, c.current, c.next, nil, nil)
			if err != nil {
				t.Fatalf("CheckDowngrade() error: %v", err)
			}
			if status != DowngradeStatusNone {
				t.Errorf("status = %v, want DowngradeStatusNone", status)
			}
			if len(blocked) != 0 {
				t.Errorf("blocked = %v, want none", blocked)
			}
		})
	}
}

func TestCheckDowngrade_InvalidVersionsFail(t *testing.T) {
	sess, engine := setupTenantSchema(t, "downgrade_badversion")

	if _, _, err := engine.CheckDowngrade(context.Background(), sess, "not-a-version", "1.0.0", nil, nil); err == nil {
		t.Error("expected an error for an invalid current version")
	}
	if _, _, err := engine.CheckDowngrade(context.Background(), sess, "1.0.0", "not-a-version", nil, nil); err == nil {
		t.Error("expected an error for an invalid new version")
	}
}

func TestCheckDowngrade_SupersetSafe(t *testing.T) {
	sess, engine := setupTenantSchema(t, "downgrade_superset")

	wide := *model.Define("sales.widget", model.Table("widgets")).
		WithStandardFields().
		Field("name", model.Text().Required()).
		Field("notes", model.Text()). // nullable — the older version won't declare this
		Index("idx_widgets_name", model.BTreeIndex("name"))
	changes, err := engine.Diff(context.Background(), sess, []model.ModelDeclaration{wide}, nil)
	if err != nil {
		t.Fatalf("Diff() error: %v", err)
	}
	if _, _, err := engine.ExecuteAccepted(context.Background(), sess, []model.ModelDeclaration{wide}, changes, nil); err != nil {
		t.Fatalf("Execute() error: %v", err)
	}

	narrow := *model.Define("sales.widget", model.Table("widgets")).
		WithStandardFields().
		Field("name", model.Text().Required())

	status, blocked, err := engine.CheckDowngrade(context.Background(), sess, "2.0.0", "1.0.0", []model.ModelDeclaration{narrow}, nil)
	if err != nil {
		t.Fatalf("CheckDowngrade() error: %v", err)
	}
	if status != DowngradeStatusSupersetSafe {
		t.Errorf("status = %v, blocked = %v, want DowngradeStatusSupersetSafe", status, blocked)
	}
	if len(blocked) != 0 {
		t.Errorf("blocked = %v, want none", blocked)
	}
}

func TestCheckDowngrade_BlockedNotNullColumnWithNoDefault(t *testing.T) {
	sess, engine := setupTenantSchema(t, "downgrade_notnull")
	conn, _ := openTestPool(t, 5*time.Second)

	base := *model.Define("sales.widget", model.Table("widgets")).
		WithStandardFields().
		Field("name", model.Text().Required())
	changes, err := engine.Diff(context.Background(), sess, []model.ModelDeclaration{base}, nil)
	if err != nil {
		t.Fatalf("Diff() error: %v", err)
	}
	if _, _, err := engine.ExecuteAccepted(context.Background(), sess, []model.ModelDeclaration{base}, changes, nil); err != nil {
		t.Fatalf("Execute() error: %v", err)
	}

	// Added directly — Execute's own classification would never apply a
	// NOT NULL column with no default automatically.
	if _, err := conn.Exec(`ALTER TABLE tenant_downgrade_notnull.widgets ADD COLUMN priority integer NOT NULL DEFAULT 0`); err != nil {
		t.Fatalf("add priority column: %v", err)
	}
	if _, err := conn.Exec(`ALTER TABLE tenant_downgrade_notnull.widgets ALTER COLUMN priority DROP DEFAULT`); err != nil {
		t.Fatalf("drop priority default: %v", err)
	}

	status, blocked, err := engine.CheckDowngrade(context.Background(), sess, "2.0.0", "1.0.0", []model.ModelDeclaration{base}, nil)
	if err != nil {
		t.Fatalf("CheckDowngrade() error: %v", err)
	}
	if status != DowngradeStatusBlocked {
		t.Fatalf("status = %v, want DowngradeStatusBlocked", status)
	}
	if len(blocked) != 1 || !strings.Contains(blocked[0], "priority") {
		t.Errorf("blocked = %v, want exactly one entry naming the priority column", blocked)
	}
}

func TestCheckDowngrade_BlockedMissingTable(t *testing.T) {
	sess, engine := setupTenantSchema(t, "downgrade_missingtable")

	target := *model.Define("sales.widget", model.Table("widgets")).
		WithStandardFields().
		Field("name", model.Text().Required())

	// Live schema is empty — the target version declares a table that was
	// never created.
	status, blocked, err := engine.CheckDowngrade(context.Background(), sess, "2.0.0", "1.0.0", []model.ModelDeclaration{target}, nil)
	if err != nil {
		t.Fatalf("CheckDowngrade() error: %v", err)
	}
	if status != DowngradeStatusBlocked {
		t.Fatalf("status = %v, want DowngradeStatusBlocked", status)
	}
	if len(blocked) != 1 || !strings.Contains(blocked[0], "widgets") {
		t.Errorf("blocked = %v, want exactly one entry naming the widgets table", blocked)
	}
}

func TestCheckDowngrade_BlockedMissingColumn(t *testing.T) {
	sess, engine := setupTenantSchema(t, "downgrade_missingcolumn")

	live := *model.Define("sales.widget", model.Table("widgets")).
		WithStandardFields().
		Field("name", model.Text().Required())
	changes, err := engine.Diff(context.Background(), sess, []model.ModelDeclaration{live}, nil)
	if err != nil {
		t.Fatalf("Diff() error: %v", err)
	}
	if _, _, err := engine.ExecuteAccepted(context.Background(), sess, []model.ModelDeclaration{live}, changes, nil); err != nil {
		t.Fatalf("Execute() error: %v", err)
	}

	// The downgrade target declares a column the live schema never had —
	// e.g. a column an intermediate version dropped.
	target := *model.Define("sales.widget", model.Table("widgets")).
		WithStandardFields().
		Field("name", model.Text().Required()).
		Field("legacy_code", model.Text())

	status, blocked, err := engine.CheckDowngrade(context.Background(), sess, "2.0.0", "1.0.0", []model.ModelDeclaration{target}, nil)
	if err != nil {
		t.Fatalf("CheckDowngrade() error: %v", err)
	}
	if status != DowngradeStatusBlocked {
		t.Fatalf("status = %v, want DowngradeStatusBlocked", status)
	}
	if len(blocked) != 1 || !strings.Contains(blocked[0], "legacy_code") {
		t.Errorf("blocked = %v, want exactly one entry naming the legacy_code column", blocked)
	}
}
