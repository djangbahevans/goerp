package tenantexport

import (
	"bytes"
	"encoding/json/v2"
	"fmt"
	"io"
	"strings"
	"testing"

	enginemanifest "github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/tenant/transfer"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
	"github.com/djangbahevans/goerp/sdk/go/model"
	"github.com/djangbahevans/goerp/sdk/go/perm"
)

func TestMany2ManyArchiveOwnershipSecurityAndCounts(t *testing.T) {
	f := newExportTestFixture(t)
	md := widgetModelDecl()
	md.Fields = append(md.Fields,
		model.NamedField{Name: "tag_ids", Def: model.Many2Many("testmodule.tag", model.Through("widget_tags"), model.M2MColumn1("widget_id"), model.M2MColumn2("tag_id"))},
		model.NamedField{Name: "private_tag_ids", Def: model.Many2Many("testmodule.tag", model.Through("private_tags"), model.M2MColumn1("widget_id"), model.M2MColumn2("tag_id")).Access(model.AccessRead(perm.Ref("testmodule:widget:private")))},
		model.NamedField{Name: "empty_tag_ids", Def: model.Many2Many("testmodule.tag", model.Through("empty_tags"), model.M2MColumn1("widget_id"), model.M2MColumn2("tag_id"))},
	)
	modules := f.worker.Registry.Snapshot().Modules()
	modules["testmodule"].Manifest.Schema.OwnedModels = []string{"testmodule.widget", "testmodule.tag"}
	modules["testmodule"].ModelDecls = []model.ModelDeclaration{md, *model.Define("tag", model.Table("tags")).Field("id", model.UUID().PrimaryKey())}
	modules["extension"] = &module.LoadedModule{
		Status: module.StatusReady,
		Manifest: enginemanifest.Manifest{
			Name: "extension", Type: "field_extension", Version: "1.0.0", DependsOn: []string{"testmodule"},
			Schema: enginemanifest.SchemaConfig{ExtendsModule: new("testmodule"), ExtendsModels: []string{"testmodule.widget"}},
		},
		ModelExtensions: []model.ModelExtension{model.Extend("testmodule.widget").Field("extra_tag_ids", model.Many2Many("testmodule.tag", model.Through("extra_tags"), model.M2MColumn1("widget_id"), model.M2MColumn2("tag_id")))},
	}
	if _, err := f.worker.Registry.Update(modules); err != nil {
		t.Fatal(err)
	}

	schema := tenantschema.Name(f.tenantSlug)
	for _, query := range []string{
		"CREATE TABLE " + schema + ".tags (id uuid PRIMARY KEY)",
		"INSERT INTO " + schema + ".tags VALUES ('22222222-2222-2222-2222-222222222222')",
	} {
		if _, err := f.worker.RawDB.ExecContext(t.Context(), query); err != nil {
			t.Fatal(err)
		}
	}
	for _, table := range []string{"widget_tags", "private_tags", "empty_tags", "extra_tags"} {
		if _, err := f.worker.RawDB.ExecContext(t.Context(), "CREATE TABLE "+schema+"."+table+" (widget_id uuid REFERENCES "+schema+".widgets, tag_id uuid REFERENCES "+schema+".tags, PRIMARY KEY (widget_id, tag_id))"); err != nil {
			t.Fatal(err)
		}
		if table == "empty_tags" {
			continue
		}
		if _, err := f.worker.RawDB.ExecContext(t.Context(), "INSERT INTO "+schema+"."+table+" VALUES ('11111111-1111-1111-1111-111111111111', '22222222-2222-2222-2222-222222222222')"); err != nil {
			t.Fatal(err)
		}
	}

	zr, jobID := exportArchive(t, f)
	entries := make(map[string][]byte)
	for _, file := range zr.File {
		rc, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		entries[file.Name], err = io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
	var man manifest
	if err := json.Unmarshal(entries["manifest.json"], &man); err != nil {
		t.Fatal(err)
	}
	if len(man.Relations) != 3 || len(man.OmittedRelations) != 0 {
		t.Fatalf("relations=%+v omissions=%+v", man.Relations, man.OmittedRelations)
	}
	for _, relation := range man.Relations {
		want := int64(1)
		if relation.Field == "empty_tag_ids" {
			want = 0
		}
		if relation.Count != want {
			t.Errorf("%s count=%d, want %d", relation.Field, relation.Count, want)
		}
	}
	for _, name := range []string{"testmodule", "extension"} {
		content := entries[name+".jsonl"]
		if checksumHex(content) != man.Checksums[name+".jsonl"] {
			t.Errorf("checksum does not cover %s links", name)
		}
		if bytes.Contains(content, []byte("private_tag_ids")) {
			t.Fatal("restricted relation is exported")
		}
	}
	if !strings.Contains(string(entries["extension.jsonl"]), `"field":"extra_tag_ids"`) || bytes.Contains(entries["testmodule.jsonl"], []byte("extra_tag_ids")) {
		t.Fatal("extension relation is not owned by the extension's data file")
	}

	if _, err := f.worker.RawDB.ExecContext(t.Context(), "DELETE FROM "+schema+".widget_tags"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.worker.run(t.Context(), testJob(jobID, Args{TenantID: f.tenantID, TenantSlug: f.tenantSlug})); err != nil {
		t.Fatal(err)
	}
	rc, _, err := f.worker.StorageBackend.Download(t.Context(), fmt.Sprintf("exports/%s/%d/modules/testmodule.jsonl", f.tenantID, jobID))
	if err != nil {
		t.Fatal(err)
	}
	staged, err := io.ReadAll(rc)
	_ = rc.Close()
	if err != nil || !bytes.Equal(staged, entries["testmodule.jsonl"]) {
		t.Fatalf("retry changed completed module data: %v", err)
	}

	var relations []transfer.Relation
	for name := range modules {
		relations = append(relations, transfer.Relations(name, f.worker.Registry.Snapshot().Models(name))...)
	}
	included, omitted := transfer.SelectRelations(relations, []string{"extension"})
	if len(included) != 0 || len(omitted) != 1 || omitted[0].MissingModules[0] != "testmodule" {
		t.Fatalf("partial extension export: included=%+v omitted=%+v", included, omitted)
	}
}
