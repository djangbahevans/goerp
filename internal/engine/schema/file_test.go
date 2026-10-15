package schema

import (
	"context"
	"fmt"
	"testing"
	"time"
	"uuid"

	"ariga.io/atlas/sql/schema"
	"github.com/djangbahevans/goerp/internal/engine/files"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/modelmeta"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

func TestFileForeignKey(t *testing.T) {
	md := model.Define("contract").Field("id", model.UUID().PrimaryKey()).Field("signed_pdf_id", model.File().Required())
	declared, err := ToAtlasSchema("tenant_acme", "contracts", []model.ModelDeclaration{*md}, nil)
	if err != nil {
		t.Fatal(err)
	}

	table, _ := declared.Table("contract")
	col, _ := table.Column("signed_pdf_id")
	if _, ok := col.Type.Type.(*schema.UUIDType); !ok || col.Type.Null {
		t.Fatalf("file column: %+v", col)
	}
	if len(declared.Tables) != 1 || len(table.ForeignKeys) != 1 {
		t.Fatalf("declared schema: %+v", declared)
	}

	fk := table.ForeignKeys[0]
	if fk.RefTable.Name != "files" || fk.RefTable.Schema.Name != "tenant_acme" || fk.RefColumns[0].Name != "id" || fk.OnDelete != schema.Restrict {
		t.Fatalf("file foreign key: %+v", fk)
	}
}

func TestFileInvalidDeclarations(t *testing.T) {
	for _, test := range []struct {
		name  string
		field string
		def   model.FieldDef
		extra string
	}{
		{name: "suffix", field: "document", def: model.File()},
		{name: "empty stem", field: "_id", def: model.File()},
		{name: "target", field: "document_id", def: model.FieldDef{Kind: model.KindFile, RelatedModel: "docs.file"}},
		{name: "domain", field: "document_id", def: model.File().Domain("true")},
		{name: "cascade", field: "document_id", def: model.File().OnDelete(model.Cascade)},
		{name: "collision", field: "document_id", def: model.File(), extra: "document"},
	} {
		t.Run(test.name, func(t *testing.T) {
			md := model.Define("contract").Field(test.field, test.def)
			if test.extra != "" {
				md.Field(test.extra, model.Text())
			}

			if _, err := ToAtlasSchema("tenant_acme", "contracts", []model.ModelDeclaration{*md}, nil); err == nil {
				t.Fatal("invalid File declaration accepted")
			}
		})
	}
}

func TestFileRejectsNonPersistentModels(t *testing.T) {
	for _, option := range []model.ModelOption{model.Transient(time.Hour), model.Virtual()} {
		md := model.Define("contract", option).Field("signed_pdf_id", model.File())
		if err := modelmeta.ValidateFile(*md, md.Fields[0]); err == nil {
			t.Fatalf("file accepted backend %q", md.Backend)
		}
	}
}

func TestFileSchemaDiffRetainsEngineFiles(t *testing.T) {
	slug := fmt.Sprintf("filediff%d", time.Now().UnixNano())
	sess, engine := setupTenantSchema(t, slug)
	conn, _ := openTestPool(t, 5*time.Second)
	if err := files.NewStore(conn).Bootstrap(t.Context(), slug); err != nil {
		t.Fatal(err)
	}

	models := []model.ModelDeclaration{*model.Define("contract").Field("id", model.UUID().PrimaryKey()).Field("signed_pdf_id", model.File()).Field("attachment_ids", model.FileMulti("contract_attachments"))}
	changes, err := engine.Diff(t.Context(), sess, models, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := engine.ExecuteAccepted(t.Context(), sess, models, changes, nil); err != nil {
		t.Fatal(err)
	}

	if !tableExists(t, conn, "tenant_"+slug, "files") {
		t.Fatal("engine files table removed")
	}
	changes, err = engine.Diff(t.Context(), sess, models, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 0 {
		t.Fatalf("file schema is not stable: %+v", changes)
	}

	if _, err := conn.ExecContext(t.Context(), "INSERT INTO tenant_"+slug+".contract (id, signed_pdf_id) VALUES (uuidv7(), uuidv7())"); err == nil {
		t.Fatal("file FK accepted missing file")
	}
}

func TestFileExtensionSchemaSync(t *testing.T) {
	conn, pool := openTestPool(t, 5*time.Second)
	slug := fmt.Sprintf("fileext%d", time.Now().UnixNano())
	if err := tenantschema.Create(t.Context(), conn, slug); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tenantschema.Drop(context.Background(), conn, slug) })
	if err := files.NewStore(conn).Bootstrap(t.Context(), slug); err != nil {
		t.Fatal(err)
	}

	md := *model.Define("contact", model.Table("contacts")).Field("id", model.UUID().PrimaryKey())
	engine := NewSchemaDiffEngine(&Config{ModelSource: fakeModelSource{"contacts": {md}}})
	tenantID := uuid.NewV7().String()
	base := &manifest.Manifest{Name: "contacts", Version: "1.0.0", Type: "domain", Schema: manifest.SchemaConfig{OwnedModels: []string{"contacts.contact"}}}
	extension := &manifest.Manifest{Name: "industry", Version: "1.0.0", Type: "field_extension", DependsOn: []string{"contacts"}, Schema: manifest.SchemaConfig{ExtendsModule: new("contacts"), ExtendsModels: []string{"contacts.contact"}}}

	sync := func(mf *manifest.Manifest, models []model.ModelDeclaration, extensions ...model.ModelExtension) {
		t.Helper()
		session, err := pool.BeginSync(t.Context(), tenantID, slug, mf.Name, mf)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = session.Close(context.Background()) }()

		changes, err := engine.Diff(t.Context(), session, models, nil, extensions...)
		if err != nil {
			t.Fatal(err)
		}
		if blocked, _, err := engine.ExecuteAccepted(t.Context(), session, models, changes, nil); err != nil || len(blocked) != 0 {
			t.Fatalf("schema sync: blocked=%v, err=%v", blocked, err)
		}
	}
	sync(base, []model.ModelDeclaration{md})
	sync(extension, nil, model.Extend("contacts.contact").Field("avatar_id", model.File()).Field("attachment_ids", model.FileMulti("contact_attachments")))
	sync(extension, nil, model.Extend("contacts.contact").Field("avatar_id", model.File()).Field("attachment_ids", model.FileMulti("contact_attachments")))
	sync(base, []model.ModelDeclaration{md})

	if _, err := conn.ExecContext(t.Context(), "INSERT INTO tenant_"+slug+".contacts (id, avatar_id) VALUES (uuidv7(), uuidv7())"); err == nil {
		t.Fatal("extension file FK accepted missing file")
	}
}

func TestFileMultiJoinSchema(t *testing.T) {
	md := model.Define("order", model.Table("sales_orders")).Field("id", model.UUID().PrimaryKey()).Field("attachment_ids", model.FileMulti("order_attachments").Required())
	s, err := ToAtlasSchema("tenant_acme", "sales", []model.ModelDeclaration{*md}, nil)
	if err != nil {
		t.Fatal(err)
	}
	owner, _ := s.Table("sales_orders")
	if _, found := owner.Column("attachment_ids"); found {
		t.Fatal("attachments created a scalar column")
	}
	join, found := s.Table("order_attachments")
	if !found || len(s.Tables) != 2 || len(join.PrimaryKey.Parts) != 2 || len(join.ForeignKeys) != 2 {
		t.Fatalf("join: %+v", join)
	}
	for i, name := range []string{"order_id", "file_id"} {
		if join.Columns[i].Name != name || join.Columns[i].Type.Null || join.ForeignKeys[i].OnDelete != schema.Cascade {
			t.Fatalf("join column %d: %+v", i, join)
		}
	}
	if join.ForeignKeys[1].RefTable.Name != "files" {
		t.Fatal("file target is not engine files")
	}
}

func TestFileMultiInvalidDeclarations(t *testing.T) {
	for _, test := range []struct {
		name  string
		field string
		def   model.FieldDef
		extra string
	}{
		{name: "suffix", field: "attachments", def: model.FileMulti("links")},
		{name: "empty stem", field: "_ids", def: model.FileMulti("links")},
		{name: "table", field: "attachment_ids", def: model.FileMulti("")},
		{name: "reserved", field: "attachment_ids", def: model.FileMulti("files")},
		{name: "domain", field: "attachment_ids", def: model.FileMulti("links").Domain("true")},
		{name: "cascade", field: "attachment_ids", def: model.FileMulti("links").OnDelete(model.Cascade)},
		{name: "collision", field: "attachment_ids", def: model.FileMulti("links"), extra: "attachments"},
		{name: "computed", field: "attachment_ids", def: model.FileMulti("links").Computed("compute")},
	} {
		t.Run(test.name, func(t *testing.T) {
			md := model.Define("order").Field("id", model.UUID().PrimaryKey()).Field(test.field, test.def)
			if test.extra != "" {
				md.Field(test.extra, model.Text())
			}
			if _, err := ToAtlasSchema("tenant_acme", "sales", []model.ModelDeclaration{*md}, nil); err == nil {
				t.Fatal("invalid declaration accepted")
			}
		})
	}
	for _, option := range []model.ModelOption{model.Virtual(), model.Transient(time.Hour)} {
		md := model.Define("order", option).Field("id", model.UUID().PrimaryKey()).Field("attachment_ids", model.FileMulti("links"))
		if err := modelmeta.ValidateMany2Many(*md, md.Fields[1]); err == nil {
			t.Fatal("nonpersistent owner accepted")
		}
	}
}
