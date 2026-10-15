package tenantimport

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json/v2"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"
	"uuid"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/checkpoint"
	"github.com/djangbahevans/goerp/internal/engine/files"
	"github.com/djangbahevans/goerp/internal/engine/jobqueue"
	enginemanifest "github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/storage"
	tenantexport "github.com/djangbahevans/goerp/internal/engine/tenant/export"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
	"github.com/djangbahevans/goerp/sdk/go/model"
	"github.com/djangbahevans/goerp/sdk/go/perm"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivertest"
)

func many2ManyModules() map[string]*module.LoadedModule {
	md := widgetModelDecl()
	md.Fields = append(md.Fields, model.NamedField{Name: "attachment_ids", Def: model.FileMulti("widget_files")})
	for _, field := range []struct{ name, target, table string }{
		{"tag_ids", "aaa.tag", "widget_tags"},
		{"external_tag_ids", "zzz.tag", "external_tags"},
		{"private_tag_ids", "zzz.tag", "private_tags"},
	} {
		def := model.Many2Many(field.target, model.Through(field.table), model.M2MColumn1("widget_id"), model.M2MColumn2("tag_id"))
		if field.name == "private_tag_ids" {
			def = def.Access(model.AccessRead(perm.Ref("aaa:widget:private")))
		}
		md.Fields = append(md.Fields, model.NamedField{Name: field.name, Def: def})
	}
	return map[string]*module.LoadedModule{
		"aaa": {
			Status:     module.StatusReady,
			Manifest:   enginemanifest.Manifest{Name: "aaa", Type: "standard", Version: "1.0.0", DependsOn: []string{"zzz"}, Schema: enginemanifest.SchemaConfig{OwnedModels: []string{"aaa.widget", "aaa.tag"}}},
			ModelDecls: []model.ModelDeclaration{md, *model.Define("tag", model.Table("tags")).Field("id", model.UUID().PrimaryKey())},
		},
		"zzz": {
			Status:     module.StatusReady,
			Manifest:   enginemanifest.Manifest{Name: "zzz", Type: "standard", Version: "1.0.0"},
			ModelDecls: []model.ModelDeclaration{*model.Define("tag", model.Table("external_labels")).Field("id", model.UUID().PrimaryKey())},
		},
		"bbb": {
			Status: module.StatusReady,
			Manifest: enginemanifest.Manifest{
				Name: "bbb", Type: "field_extension", Version: "1.0.0", DependsOn: []string{"aaa", "zzz"},
				Schema: enginemanifest.SchemaConfig{ExtendsModule: new("aaa"), ExtendsModels: []string{"aaa.widget"}},
			},
			ModelExtensions: []model.ModelExtension{model.Extend("aaa.widget").Field("extra_tag_ids", model.Many2Many("zzz.tag", model.Through("extra_tags"), model.M2MColumn1("widget_id"), model.M2MColumn2("tag_id")))},
		},
	}
}

func exportRelationArchive(t *testing.T, f *importTestFixture, slug, tenantID string, exclude []string) ([]byte, []byte, string) {
	t.Helper()
	ctx := t.Context()
	var database string
	if err := f.conn.QueryRowContext(ctx, "SELECT current_database()").Scan(&database); err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.New(ctx, "postgres://goerp:dev@localhost:15432/"+database)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := jobqueue.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })
	worker := &tenantexport.Worker{TenantStore: f.worker.TenantStore, Registry: f.reg, RawDB: f.conn, Checkpoints: checkpoint.NewStore(f.conn), StorageBackend: f.storage, Keys: f.keys}
	tester := rivertest.NewWorker(t, riverpgxv5.New(pool), &river.Config{Schema: jobqueue.Schema}, worker)
	result, err := tester.Work(ctx, t, tx, tenantexport.Args{TenantSlug: slug, TenantID: tenantID, Exclude: exclude}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var metadata struct {
		Output tenantexport.Result `json:"output"`
	}
	if err := json.Unmarshal(result.Job.Metadata, &metadata); err != nil {
		t.Fatal(err)
	}
	rc, _, err := f.storage.Download(ctx, fmt.Sprintf("exports/%s/%d/archive.zip.enc", tenantID, result.Job.ID))
	if err != nil {
		t.Fatal(err)
	}
	ciphertext, err := io.ReadAll(rc)
	_ = rc.Close()
	if err != nil {
		t.Fatal(err)
	}
	key, err := f.keys.Decrypt([]byte(metadata.Output.DecryptionKey))
	if err != nil {
		t.Fatal(err)
	}
	plain, err := decryptArchive(ciphertext, string(key))
	if err != nil {
		t.Fatal(err)
	}
	return plain, ciphertext, string(key)
}

func TestMany2ManyRoundTripAndPartialExport(t *testing.T) {
	for _, tc := range []struct {
		name      string
		exclude   []string
		relations int
		omissions int
	}{
		{name: "complete", relations: 4},
		{name: "target_excluded", exclude: []string{"zzz"}, relations: 2, omissions: 2},
		{name: "source_excluded", exclude: []string{"aaa"}, omissions: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newImportTestFixture(t)
			if _, err := f.reg.Update(many2ManyModules()); err != nil {
				t.Fatal(err)
			}
			slug := "m2msrc" + strings.ReplaceAll(uuid.NewV7().String(), "-", "")
			source, err := f.worker.TenantStore.CreateTenant(t.Context(), slug, "Source")
			if err != nil {
				t.Fatal(err)
			}
			if err := f.worker.Provision.CreateTenantSchema(t.Context(), slug); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = tenantschema.Drop(context.Background(), f.conn, slug) })
			if err := f.worker.Provision.CreateEngineTables(t.Context(), slug); err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"zzz", "aaa", "bbb"} {
				if err := f.worker.Provision.SyncModuleSchema(t.Context(), source.ID, slug, name, abiv1.Request{}); err != nil {
					t.Fatal(err)
				}
			}
			schema := tenantschema.Name(slug)
			widgetID := uuid.NewV7().String()
			tagIDs := []string{uuid.NewV7().String(), uuid.NewV7().String()}
			externalIDs := []string{uuid.NewV7().String(), uuid.NewV7().String()}
			fileIDs := []string{uuid.NewV7().String(), uuid.NewV7().String()}
			for _, fileID := range fileIDs {
				key := storage.BuildKey("attachments", source.ID, fileID, ".pdf")
				if _, err := f.storage.Upload(t.Context(), key, strings.NewReader("pdf"), storage.UploadOptions{}); err != nil {
					t.Fatal(err)
				}
				if err := files.NewStore(f.conn).Insert(t.Context(), slug, files.InsertRow{
					ID:             fileID,
					TenantID:       source.ID,
					StorageKey:     key,
					OriginalName:   "attachment.pdf",
					ContentType:    "application/pdf",
					SizeBytes:      3,
					ChecksumSHA256: fmt.Sprintf("%x", sha256.Sum256([]byte("pdf"))),
					Purpose:        "attachments",
				}); err != nil {
					t.Fatal(err)
				}
			}

			execSQL(t, f.conn, "INSERT INTO "+schema+".widgets (id, name) VALUES ($1, 'Widget')", widgetID)
			for i := range tagIDs {
				execSQL(t, f.conn, "INSERT INTO "+schema+".tags VALUES ($1)", tagIDs[i])
				execSQL(t, f.conn, "INSERT INTO "+schema+".external_labels VALUES ($1)", externalIDs[i])
			}
			for _, table := range []string{"widget_tags", "external_tags", "private_tags", "extra_tags", "widget_files"} {
				targetIDs := externalIDs
				if table == "widget_tags" {
					targetIDs = tagIDs
				}
				if table == "widget_files" {
					targetIDs = fileIDs
				}
				for _, targetID := range targetIDs {
					execSQL(t, f.conn, "INSERT INTO "+schema+"."+table+" VALUES ($1, $2)", widgetID, targetID)
				}
			}
			plain, ciphertext, key := exportRelationArchive(t, f, slug, source.ID, tc.exclude)
			man, data, err := parseArchive(plain)
			if err != nil {
				t.Fatal(err)
			}
			if len(man.OmittedRelations) != tc.omissions || len(man.Relations) != tc.relations {
				t.Fatalf("archive relations=%+v omissions=%+v", man.Relations, man.OmittedRelations)
			}
			for _, relation := range man.Relations {
				if relation.Count != 2 {
					t.Fatalf("relation count=%+v", relation)
				}
			}
			if strings.Contains(string(data["aaa"]), "private_tag_ids") {
				t.Fatal("restricted links exported")
			}

			newSlug := "m2mdst" + strings.ReplaceAll(uuid.NewV7().String(), "-", "")
			job := newImportJob(t, f, newSlug, ciphertext, key)
			result, err := f.worker.run(t.Context(), job)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = tenantschema.Drop(context.Background(), f.conn, newSlug) })
			for _, table := range []string{"widget_tags", "external_tags", "private_tags", "extra_tags", "widget_files"} {
				want := 2
				if table == "private_tags" || tc.name == "source_excluded" || (tc.name == "target_excluded" && table != "widget_tags" && table != "widget_files") {
					want = 0
				}
				var count int
				if err := f.conn.QueryRowContext(t.Context(), "SELECT count(*) FROM "+tenantschema.Name(newSlug)+"."+table).Scan(&count); err != nil {
					t.Fatal(err)
				}
				if count != want {
					t.Errorf("%s count=%d, want %d", table, count, want)
				}
				if want > 0 {
					targetIDs := externalIDs
					if table == "widget_tags" {
						targetIDs = tagIDs
					}
					column := "tag_id"
					if table == "widget_files" {
						targetIDs = fileIDs
						column = "file_id"
					}
					for _, targetID := range targetIDs {
						var linked bool
						if err := f.conn.QueryRowContext(t.Context(), "SELECT EXISTS (SELECT 1 FROM "+tenantschema.Name(newSlug)+"."+table+" WHERE widget_id=$1 AND "+column+"=$2)", widgetID, targetID).Scan(&linked); err != nil || !linked {
							t.Fatalf("%s link mismatch: %v", table, err)
						}
					}
				}
			}
			retried, err := f.worker.run(t.Context(), job)
			if err != nil || retried.TenantID != result.TenantID {
				t.Fatalf("retry result=%+v error=%v", retried, err)
			}
		})
	}
}

func newImportJob(t *testing.T, f *importTestFixture, slug string, ciphertext []byte, key string) *river.Job[Args] {
	t.Helper()
	jobID := time.Now().UnixNano()
	inputRef := fmt.Sprintf("imports/%d/archive.enc", jobID)
	if _, err := f.storage.Upload(t.Context(), inputRef, bytes.NewReader(ciphertext), storage.UploadOptions{}); err != nil {
		t.Fatal(err)
	}
	return testJob(jobID, Args{NewSlug: slug, InputRef: inputRef, DecryptionKey: encryptedArgsKey(t, f.keys, key)})
}

func TestMany2ManyLoadDefersLinksAndRollsBackModelRows(t *testing.T) {
	f := newImportTestFixture(t)
	slug := "m2mload" + strings.ReplaceAll(uuid.NewV7().String(), "-", "")
	if err := tenantschema.Create(t.Context(), f.conn, slug); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tenantschema.Drop(context.Background(), f.conn, slug) })
	schema := tenantschema.Name(slug)
	execSQL(t, f.conn, "CREATE TABLE "+schema+".widgets (id uuid PRIMARY KEY, name text NOT NULL)")
	execSQL(t, f.conn, "CREATE TABLE "+schema+".tags (id uuid PRIMARY KEY)")
	execSQL(t, f.conn, "CREATE TABLE "+schema+".widget_tags (widget_id uuid REFERENCES "+schema+".widgets, tag_id uuid REFERENCES "+schema+".tags, PRIMARY KEY (widget_id, tag_id))")
	md := widgetModelDecl()
	md.Fields = append(md.Fields, model.NamedField{Name: "tag_ids", Def: model.Many2Many("testmodule.tag", model.Through("widget_tags"), model.M2MColumn1("widget_id"), model.M2MColumn2("tag_id"))})
	mod := &module.LoadedModule{
		Manifest:   enginemanifest.Manifest{Name: "testmodule"},
		ModelDecls: []model.ModelDeclaration{md, *model.Define("tag", model.Table("tags")).Field("id", model.UUID().PrimaryKey())},
	}
	widgetID, tagID := uuid.NewV7().String(), uuid.NewV7().String()
	data := fmt.Appendf(nil, `{"relation":{"model":"testmodule.widget","field":"tag_ids","source_id":%q,"target_id":%q}}`+"\n", widgetID, tagID)
	data = fmt.Appendf(data, `{"model":"widget","record":{"id":%q,"name":"Widget"}}`+"\n", widgetID)
	data = fmt.Appendf(data, `{"model":"tag","record":{"id":%q}}`+"\n", tagID)
	if err := f.worker.loadModule(t.Context(), slug, mod, data); err != nil {
		t.Fatalf("load links before their rows: %v", err)
	}
	if err := f.worker.loadModule(t.Context(), slug, mod, data); err != nil {
		t.Fatalf("idempotent module retry: %v", err)
	}
	var count int
	if err := f.conn.QueryRowContext(t.Context(), "SELECT count(*) FROM "+schema+".widget_tags").Scan(&count); err != nil || count != 1 {
		t.Fatalf("restored links=%d, error=%v", count, err)
	}

	for _, field := range []string{"tag_ids", "unknown_ids"} {
		newID := uuid.NewV7().String()
		invalid := fmt.Appendf(nil, `{"model":"widget","record":{"id":%q,"name":"Rollback"}}`+"\n", newID)
		invalid = fmt.Appendf(invalid, `{"relation":{"model":"testmodule.widget","field":%q,"source_id":%q,"target_id":%q}}`+"\n", field, newID, uuid.NewV7().String())
		if err := f.worker.loadModule(t.Context(), slug, mod, invalid); err == nil {
			t.Fatalf("invalid relation %s accepted", field)
		}
		if err := f.conn.QueryRowContext(t.Context(), "SELECT count(*) FROM "+schema+".widgets WHERE id=$1", newID).Scan(&count); err != nil || count != 0 {
			t.Fatalf("relation failure did not roll back model row: count=%d, error=%v", count, err)
		}
	}
}
