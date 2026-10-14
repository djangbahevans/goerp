package wasm

import (
	"slices"
	"testing"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

func TestORMMany2ManyLinksReadOnlyTargets(t *testing.T) {
	db, r, mc := m2mFixture(t)
	id := m2mCreate(t, db, r, mc, nil)
	schema := quoteIdentORM("tenant_" + mc.TenantSlug)
	reader := openTestRLSReaderORM(t, db, schema, schema+".contact_tags")
	for _, statement := range []string{
		"ALTER TABLE " + schema + ".contact_tags ENABLE ROW LEVEL SECURITY",
		"CREATE POLICY readable ON " + schema + ".contact_tags FOR SELECT USING (true)",
		"GRANT SELECT, UPDATE ON " + schema + ".contacts TO goerp_test_rls_reader_orm",
		"GRANT SELECT, INSERT, DELETE ON " + schema + ".contact_tag_assignments TO goerp_test_rls_reader_orm",
	} {
		if _, err := db.ExecContext(t.Context(), statement); err != nil {
			t.Fatal(err)
		}
	}
	tx, commit, rollback, hostErr := resolveORMWriteTx(t.Context(), r, reader, mc, "")
	if hostErr != nil {
		t.Fatalf("begin: %+v", hostErr)
	}
	defer rollback()
	_, _, hostErr = writeOneRecordTx(t.Context(), tx, mc, mc.ModelDecls()[0], "testmodule.contact", "id", id, map[string]any{"tag_ids": []string{m2mTag1}}, nil, nil)
	if hostErr != nil {
		t.Fatalf("linking a readable tag must not require updating that tag: %+v", hostErr)
	}
	if err := commit(); err != nil {
		t.Fatal(err)
	}
	if ids := m2mRead(t, db, mc, id)["tag_ids"].([]string); !slices.Equal(ids, []string{m2mTag1}) {
		t.Fatalf("linked IDs: %v", ids)
	}
}

func TestORMMany2ManyCreateWithScalarDefaults(t *testing.T) {
	db, r, mc := m2mFixture(t)
	if _, err := db.ExecContext(t.Context(), "ALTER TABLE tenant_"+mc.TenantSlug+".contacts ALTER COLUMN name SET DEFAULT 'Default'"); err != nil {
		t.Fatal(err)
	}
	models := mc.ModelDecls()
	models[0].Fields[1].Def = model.Text().Required().Default("'Default'")
	mc = m2mContext(mc.TenantSlug, models)
	out, hostErr := ORMCreate(t.Context(), r, db, r.EventInsertClient(), nil, mc, abiv1.ORMCreateInput{
		Model:  "testmodule.contact",
		Record: map[string]any{"tag_ids": []string{m2mTag1}},
	})
	if hostErr != nil {
		t.Fatalf("create with only relation values and scalar defaults: %+v", hostErr)
	}
	row := m2mRead(t, db, mc, out.Record["id"].(string))
	if row["name"] != "Default" || !slices.Equal(row["tag_ids"].([]string), []string{m2mTag1}) {
		t.Fatalf("created row: %v", row)
	}
}
