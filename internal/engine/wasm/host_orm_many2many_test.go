package wasm

import (
	"database/sql"
	"fmt"
	"slices"
	"testing"
	"time"

	"ariga.io/atlas/sql/postgres"
	atlasschema "ariga.io/atlas/sql/schema"
	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/djangbahevans/goerp/internal/engine/fieldsec"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/permission"
	engineschema "github.com/djangbahevans/goerp/internal/engine/schema"
	"github.com/djangbahevans/goerp/sdk/go/model"
	"github.com/djangbahevans/goerp/sdk/go/perm"
)

const m2mTag1 = "11111111-1111-1111-1111-111111111111"
const m2mTag2 = "22222222-2222-2222-2222-222222222222"

func m2mFixture(t *testing.T) (*sql.DB, *Runtime, *ModuleContext) {
	t.Helper()
	db := openTestPrimaryDB(t)
	slug := fmt.Sprintf("m2m%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, db, slug)

	models := []model.ModelDeclaration{
		*model.Define("contact", model.Table("contacts")).
			Field("id", model.UUID().PrimaryKey().Default("uuidv7()")).
			Field("name", model.Text().Required()).
			Field("tag_ids", model.Many2Many("testmodule.tag", model.Through("contact_tag_assignments"), model.M2MColumn1("contact_id"), model.M2MColumn2("tag_id"))),
		*model.Define("tag", model.Table("contact_tags")).
			Field("id", model.UUID().PrimaryKey()).
			Field("name", model.Text().Required()).
			Field("color", model.Text()).
			Field("secret", model.Text().Access(model.AccessRead(perm.Ref("testmodule:tag:secret")))),
	}
	declared, err := engineschema.ToAtlasSchema("tenant_"+slug, "testmodule", models, nil)
	if err != nil {
		t.Fatal(err)
	}
	driver, err := postgres.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	changes := make([]atlasschema.Change, len(declared.Tables))
	for i, table := range declared.Tables {
		changes[i] = &atlasschema.AddTable{T: table}
	}
	plan, err := driver.PlanChanges(t.Context(), "many2many", changes)
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range plan.Changes {
		if _, err := db.ExecContext(t.Context(), change.Cmd); err != nil {
			t.Fatal(err)
		}
	}

	grantFixtureTables(t, db, slug, "contacts", "contact_tags", "contact_tag_assignments")
	if _, err := db.ExecContext(t.Context(), "INSERT INTO tenant_"+slug+".contact_tags VALUES ($1, 'VIP', '#112233', 'hidden'), ($2, 'Wholesale', '#445566', 'hidden')", m2mTag1, m2mTag2); err != nil {
		t.Fatal(err)
	}

	return db, newHostDBTestRuntime(t, db, 10), m2mContext(slug, models)
}

func m2mContext(slug string, models []model.ModelDeclaration) *ModuleContext {
	fields := fieldsec.New()
	fields.Register("testmodule", models)

	perms := permission.NewPermissionRegistry()
	perms.Register("testmodule", []manifest.Permission{
		{Name: "testmodule:contact:tags_read"},
		{Name: "testmodule:contact:tags_write"},
		{Name: "testmodule:tag:secret"},
	})
	return NewModuleContext("req", "testmodule", "user", "contact", nil, nil, "tenant", slug, "trace", abi.CapDBRead|abi.CapDBWrite, nil, ModuleSnapshot{
		ModelDecls:         models,
		FieldSecRegistry:   fields,
		PermissionRegistry: perms,
	})
}

func m2mCreate(t *testing.T, db *sql.DB, r *Runtime, mc *ModuleContext, ids []string) string {
	t.Helper()
	out, hostErr := ORMCreate(t.Context(), r, db, r.EventInsertClient(), nil, mc, abiv1.ORMCreateInput{
		Model:  "testmodule.contact",
		Record: map[string]any{"name": "Kofi", "tag_ids": ids},
	})
	if hostErr != nil {
		t.Fatalf("create: %+v", hostErr)
	}
	return out.Record["id"].(string)
}

func m2mRead(t *testing.T, db *sql.DB, mc *ModuleContext, id string, fields ...string) map[string]any {
	t.Helper()
	out, hostErr := ORMRead(t.Context(), db, nil, mc, abiv1.ORMReadInput{Model: "testmodule.contact", IDs: []string{id}, Fields: fields})
	if hostErr != nil || len(out.Records) != 1 {
		t.Fatalf("read: %+v; records: %+v", hostErr, out)
	}
	return out.Records[0]
}

func TestORMMany2ManyRoundTrip(t *testing.T) {
	db, r, mc := m2mFixture(t)
	write := newHostORMWriteCaller(t, t.Context(), r, mc)
	var created abiv1.ORMCreateOutput
	env := callORMHost(t, t.Context(), write, "call_create", abiv1.ORMCreateInput{
		Model:  "testmodule.contact",
		Record: map[string]any{"name": "Kofi", "tag_ids": []string{m2mTag2, m2mTag1, m2mTag1}},
	}, &created)
	if !env.OK {
		t.Fatalf("create ABI: %+v", env.Error)
	}

	id := created.Record["id"].(string)
	row := m2mRead(t, db, mc, id, "tag_ids")
	if _, found := row["id"]; found {
		t.Fatal("relation-only projection leaked the parent key")
	}
	if ids := row["tag_ids"].([]string); !slices.Equal(ids, []string{m2mTag1, m2mTag2}) {
		t.Fatalf("IDs: %v", ids)
	}

	tags := row["tags"].([]map[string]any)
	if len(tags) != 2 || tags[0]["name"] != "VIP" || tags[0]["color"] != "#112233" {
		t.Fatalf("tag objects: %v", tags)
	}
	if _, found := tags[0]["secret"]; found {
		t.Fatal("target field permission leaked")
	}

	for _, change := range []map[string]any{
		{"tag_ids": []string{m2mTag2}},
		{"name": "Ama"},
		{"tag_ids": []string{}},
	} {
		written, hostErr := ORMWrite(t.Context(), r, db, r.EventInsertClient(), nil, mc, abiv1.ORMWriteInput{Model: "testmodule.contact", ID: id, Record: change})
		if hostErr != nil {
			t.Fatalf("write: %+v", hostErr)
		}

		expected := []string{m2mTag2}
		if set, present := change["tag_ids"].([]string); present {
			expected = set
			if ids, ok := written.Record["tag_ids"].([]string); !ok || ids == nil || !slices.Equal(ids, expected) {
				t.Fatalf("write response tag_ids = %#v, expected %v", written.Record["tag_ids"], expected)
			}
		}

		read := m2mRead(t, db, mc, id)
		if !slices.Equal(read["tag_ids"].([]string), expected) {
			t.Fatalf("replace/omit/clear: %v, expected %v", read, expected)
		}
	}

	search, hostErr := ORMSearchRead(t.Context(), db, mc, abiv1.ORMSearchReadInput{Model: "testmodule.contact", Fields: []string{"tag_ids"}})
	if hostErr != nil || len(search.Records) != 1 || len(search.Records[0]["tags"].([]map[string]any)) != 0 {
		t.Fatalf("search read empty set: %+v, %+v", search, hostErr)
	}
}

func TestORMMany2ManyInvalidAndDeniedWritesAreAtomic(t *testing.T) {
	db, r, mc := m2mFixture(t)
	id := m2mCreate(t, db, r, mc, []string{m2mTag1})

	for _, value := range []any{nil, "bad", []any{1}, []string{"not-a-uuid"}, []string{m2mTag2, "33333333-3333-3333-3333-333333333333"}} {
		_, hostErr := ORMWrite(t.Context(), r, db, r.EventInsertClient(), nil, mc, abiv1.ORMWriteInput{Model: "testmodule.contact", ID: id, Record: map[string]any{"name": "Changed", "tag_ids": value}})
		if hostErr == nil {
			t.Fatalf("accepted invalid set: %v", value)
		}
		row := m2mRead(t, db, mc, id)
		if row["name"] != "Kofi" || !slices.Equal(row["tag_ids"].([]string), []string{m2mTag1}) {
			t.Fatalf("partially applied invalid write: %v", row)
		}
	}

	models := mc.ModelDecls()
	models[0].Fields[2].Def = models[0].Fields[2].Def.Access(model.AccessWrite(perm.Ref("testmodule:contact:tags_write")))
	denied := m2mContext(mc.TenantSlug, models)
	_, hostErr := ORMWrite(t.Context(), r, db, r.EventInsertClient(), nil, denied, abiv1.ORMWriteInput{Model: "testmodule.contact", ID: id, Record: map[string]any{"name": "Changed", "tag_ids": []string{m2mTag2}}})
	if hostErr == nil || hostErr.Code != abiv1.ErrCodeFieldWriteDenied {
		t.Fatalf("denied write: %+v", hostErr)
	}
	if row := m2mRead(t, db, mc, id); row["name"] != "Kofi" {
		t.Fatalf("parent changed on denied write: %v", row)
	}

	models[0].Fields[2].Def = models[0].Fields[2].Def.OnDeniedWrite(model.Ignore)
	ignored := m2mContext(mc.TenantSlug, models)
	if _, hostErr := ORMWrite(t.Context(), r, db, r.EventInsertClient(), nil, ignored, abiv1.ORMWriteInput{Model: "testmodule.contact", ID: id, Record: map[string]any{"name": "Ama", "tag_ids": []string{m2mTag2}}}); hostErr != nil {
		t.Fatalf("ignored write: %+v", hostErr)
	}
	if row := m2mRead(t, db, mc, id); row["name"] != "Ama" || !slices.Equal(row["tag_ids"].([]string), []string{m2mTag1}) {
		t.Fatalf("ignored write modified links: %v", row)
	}
}

func TestORMMany2ManyParentReadSecurity(t *testing.T) {
	db, r, mc := m2mFixture(t)
	id := m2mCreate(t, db, r, mc, []string{m2mTag1})

	for _, behavior := range []model.DeniedReadBehaviour{model.Omit, model.Nullify} {
		models := mc.ModelDecls()
		models[0].Fields[2].Def = models[0].Fields[2].Def.Access(model.AccessRead(perm.Ref("testmodule:contact:tags_read"))).OnDeniedRead(behavior)
		denied := m2mContext(mc.TenantSlug, models)
		row := m2mRead(t, db, denied, id)

		for _, key := range []string{"tag_ids", "tags"} {
			value, found := row[key]
			if behavior.Kind == model.ReadKindOmit && found {
				t.Fatalf("omitted relation leaked %s", key)
			}
			if behavior.Kind == model.ReadKindNullify && (!found || value != nil) {
				t.Fatalf("nullified relation: %v", row)
			}
		}
	}
}

func TestORMMany2ManyRLSAndTenantIsolation(t *testing.T) {
	db, r, mc := m2mFixture(t)
	id := m2mCreate(t, db, r, mc, []string{m2mTag1, m2mTag2})

	schema := "tenant_" + mc.TenantSlug
	if _, err := db.ExecContext(t.Context(), "ALTER TABLE "+schema+".contact_tags ENABLE ROW LEVEL SECURITY"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), "CREATE POLICY visible ON "+schema+".contact_tags FOR ALL USING (name = 'VIP')"); err != nil {
		t.Fatal(err)
	}

	reader := openTestRLSReaderORM(t, db, schema, schema+".contact_tags")
	if _, err := db.ExecContext(t.Context(), "GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA "+schema+" TO goerp_test_rls_reader_orm"); err != nil {
		t.Fatal(err)
	}

	row := m2mRead(t, reader, mc, id)
	if !slices.Equal(row["tag_ids"].([]string), []string{m2mTag1}) || len(row["tags"].([]map[string]any)) != 1 {
		t.Fatalf("RLS-hidden target leaked: %v", row)
	}
	if _, hostErr := ORMWrite(t.Context(), r, reader, r.EventInsertClient(), nil, mc, abiv1.ORMWriteInput{Model: "testmodule.contact", ID: id, Record: map[string]any{"name": "Changed", "tag_ids": []string{m2mTag2}}}); hostErr == nil {
		t.Fatal("linked an RLS-hidden target")
	}
	if row := m2mRead(t, reader, mc, id); row["name"] != "Kofi" {
		t.Fatalf("RLS rejection changed parent: %v", row)
	}

	otherDB, _, other := m2mFixture(t)
	const foreignID = "44444444-4444-4444-4444-444444444444"
	if _, err := otherDB.ExecContext(t.Context(), "INSERT INTO tenant_"+other.TenantSlug+".contact_tags (id, name) VALUES ($1, 'Foreign')", foreignID); err != nil {
		t.Fatal(err)
	}

	if _, hostErr := ORMWrite(t.Context(), r, reader, r.EventInsertClient(), nil, mc, abiv1.ORMWriteInput{Model: "testmodule.contact", ID: id, Record: map[string]any{"tag_ids": []string{foreignID}}}); hostErr == nil {
		t.Fatal("linked a record from another tenant")
	}
}

func TestORMMany2ManyReportsOnlyChangedSets(t *testing.T) {
	db, r, mc := m2mFixture(t)
	id := m2mCreate(t, db, r, mc, []string{m2mTag1})
	md := mc.ModelDecls()[0]

	for _, tc := range []struct {
		ids     []string
		changed []string
	}{
		{[]string{m2mTag1}, nil},
		{[]string{m2mTag1, m2mTag2}, []string{"tag_ids"}},
		{[]string{}, []string{"tag_ids"}},
	} {
		tx, commit, rollback, hostErr := resolveORMWriteTx(t.Context(), r, db, mc, "")
		if hostErr != nil {
			t.Fatalf("begin: %+v", hostErr)
		}
		_, changed, hostErr := writeOneRecordTx(t.Context(), tx, mc, md, "testmodule.contact", "id", id, map[string]any{"tag_ids": tc.ids}, nil, nil)
		if hostErr != nil {
			rollback()
			t.Fatalf("write %v: %+v", tc.ids, hostErr)
		}
		if err := commit(); err != nil {
			t.Fatal(err)
		}

		if !slices.Equal(changed, tc.changed) {
			t.Fatalf("write %v: changed = %v, want %v", tc.ids, changed, tc.changed)
		}
	}
}
