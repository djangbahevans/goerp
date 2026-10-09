package schema

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
	"uuid"

	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

func TestModelExtensionSchemaSyncRole(t *testing.T) {
	admin, _ := openTestPool(t, time.Second)
	database := "modelextrole_" + strings.ReplaceAll(uuid.NewV7().String(), "-", "")
	if _, err := admin.ExecContext(t.Context(), "CREATE DATABASE "+quoteIdent(database)+" OWNER schema_sync_user"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec("DROP DATABASE " + quoteIdent(database) + " WITH (FORCE)"); err != nil {
			t.Errorf("drop private database: %v", err)
		}
	})

	conn, err := db.New("postgres://schema_sync_user:dev@localhost:15432/" + database)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	pool := NewPool(conn, time.Second)
	if err := pool.Bootstrap(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(t.Context(), "CREATE SCHEMA tenant_extensionrole"); err != nil {
		t.Fatal(err)
	}

	decl := *model.Define("contacts.contact", model.Table("contacts")).
		Field("id", model.UUID().PrimaryKey().Default("uuidv7()")).
		Field("name", model.Text())
	engine := NewSchemaDiffEngine(&Config{ModelSource: fakeModelSource{"contacts": {decl}}})
	tenantID := uuid.NewV7().String()
	base := &manifest.Manifest{Name: "contacts", Version: "1.0.0", Type: "domain", Schema: manifest.SchemaConfig{OwnedModels: []string{"contacts.contact"}}}
	extension := &manifest.Manifest{
		Name: "industry", Version: "1.0.0", Type: "field_extension", DependsOn: []string{"contacts"},
		Schema: manifest.SchemaConfig{ExtendsModule: new("contacts"), ExtendsModels: []string{"contacts.contact"}},
	}
	sync := func(mf *manifest.Manifest, models []model.ModelDeclaration, extensions ...model.ModelExtension) {
		t.Helper()
		sess, err := pool.BeginSync(t.Context(), tenantID, "extensionrole", mf.Name, mf)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = sess.Close(context.Background()) }()

		changes, err := engine.Diff(t.Context(), sess, models, nil, extensions...)
		if err != nil {
			t.Fatal(err)
		}
		blocked, _, err := engine.ExecuteAccepted(t.Context(), sess, models, changes, nil)
		if err != nil || len(blocked) != 0 {
			t.Fatalf("sync %s: blocked=%v, err=%v", mf.Name, blocked, err)
		}
	}
	sync(base, []model.ModelDeclaration{decl})
	sync(extension, nil, model.Extend("contacts.contact").Field("industry", model.Text()).Index("industry_idx", model.BTreeIndex("industry")))
	sync(base, []model.ModelDeclaration{decl})

	var owner string
	if err := conn.QueryRowContext(t.Context(), `SELECT pg_get_userbyid(relowner) FROM pg_class WHERE oid = 'system.model_extension_objects'::regclass`).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	if owner != "schema_sync_user" || !columnExists(t, conn, "tenant_extensionrole", "contacts", "industry") {
		t.Fatalf("schema-sync ownership=%s or extension column missing", owner)
	}
}

func TestModelExtensionSyncSharesBaseLock(t *testing.T) {
	conn, pool := openTestPool(t, time.Second)
	base := &manifest.Manifest{Name: "contacts", Type: "domain"}
	extension := &manifest.Manifest{Name: "industry", Type: "field_extension", Schema: manifest.SchemaConfig{ExtendsModule: new("contacts")}}
	tenantID := uuid.NewV7().String()
	slug := fmt.Sprintf("extensionlock%d", time.Now().UnixNano())
	sess, err := pool.BeginSync(t.Context(), tenantID, slug, base.Name, base)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sess.Close(context.Background()) }()

	short := NewPool(conn, 50*time.Millisecond)
	other, err := short.BeginSync(t.Context(), tenantID, slug, extension.Name, extension)
	if err == nil {
		_ = other.Close(t.Context())
		t.Fatal("extension sync acquired a lock held by base sync")
	}
	if !strings.Contains(err.Error(), "timed out waiting") {
		t.Fatalf("extension lock error: %v", err)
	}
}
