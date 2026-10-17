package wasm

import (
	"slices"
	"testing"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

func TestORMMany2ManyArchiveVisibilityAndRetention(t *testing.T) {
	db, r, mc := m2mFixture(t)
	id := m2mCreate(t, db, r, mc, []string{m2mTag1})
	other := m2mCreate(t, db, r, mc, nil)
	if _, err := db.ExecContext(t.Context(), "ALTER TABLE tenant_"+mc.TenantSlug+".contact_tags ADD deleted_at timestamptz"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), "UPDATE tenant_"+mc.TenantSlug+".contact_tags SET deleted_at = NOW() WHERE id = $1", m2mTag1); err != nil {
		t.Fatal(err)
	}

	models := mc.ModelDecls()
	models[1].Fields = append(models[1].Fields, model.NamedField{Name: "deleted_at", Def: model.TimestampTZ()})
	mc = m2mContext(mc.TenantSlug, models)
	if row := m2mRead(t, db, mc, id); len(row["tag_ids"].([]string)) != 0 || len(row["tags"].([]map[string]any)) != 0 {
		t.Fatalf("default relation exposes archived target: %v", row)
	}

	models[0].Fields[2].Def.RetainArchived = true
	mc = m2mContext(mc.TenantSlug, models)
	if row := m2mRead(t, db, mc, id); !slices.Equal(row["tag_ids"].([]string), []string{m2mTag1}) || len(row["tags"].([]map[string]any)) != 1 {
		t.Fatalf("retained relation omits archived target: %v", row)
	}

	_, hostErr := ORMWrite(t.Context(), r, db, r.EventInsertClient(), nil, mc, abiv1.ORMWriteInput{
		Model: "testmodule.contact", ID: id, Record: map[string]any{"tag_ids": []string{m2mTag1, m2mTag2}},
	})
	if hostErr != nil {
		t.Fatalf("retain archived and add active: %+v", hostErr)
	}
	_, hostErr = ORMWrite(t.Context(), r, db, r.EventInsertClient(), nil, mc, abiv1.ORMWriteInput{
		Model: "testmodule.contact", ID: other, Record: map[string]any{"name": "Must roll back", "tag_ids": []string{m2mTag1}},
	})
	if hostErr == nil || hostErr.Code != abiv1.ErrCodeValidationFailed {
		t.Fatalf("added archived target: %+v", hostErr)
	}
	if row := m2mRead(t, db, mc, other); row["name"] != "Kofi" {
		t.Fatalf("rejected assignment changed parent: %v", row)
	}

	schema := "tenant_" + mc.TenantSlug
	reader := openTestRLSReaderORM(t, db, schema, schema+".contact_tags")
	for _, statement := range []string{
		"ALTER TABLE " + schema + ".contact_tags ENABLE ROW LEVEL SECURITY",
		"CREATE POLICY active_only ON " + schema + ".contact_tags FOR SELECT USING (deleted_at IS NULL)",
		"GRANT SELECT, UPDATE ON " + schema + ".contacts TO goerp_test_rls_reader_orm",
		"GRANT SELECT, INSERT, DELETE ON " + schema + ".contact_tag_assignments TO goerp_test_rls_reader_orm",
	} {
		if _, err := db.ExecContext(t.Context(), statement); err != nil {
			t.Fatal(err)
		}
	}
	if row := m2mRead(t, reader, mc, id); !slices.Equal(row["tag_ids"].([]string), []string{m2mTag2}) {
		t.Fatalf("retention bypasses target RLS: %v", row)
	}
	_, hostErr = ORMWrite(t.Context(), r, reader, r.EventInsertClient(), nil, mc, abiv1.ORMWriteInput{
		Model: "testmodule.contact", ID: id, Record: map[string]any{"tag_ids": []string{m2mTag1, m2mTag2}},
	})
	if hostErr == nil || hostErr.Code != abiv1.ErrCodeValidationFailed {
		t.Fatalf("retained unreadable archived target: %+v", hostErr)
	}

	for _, source := range []string{
		"record.tag_ids = '" + m2mTag1 + "'",
		"record.tag_ids IN ('" + m2mTag1 + "')",
	} {
		out, hostErr := ORMSearchRead(t.Context(), db, mc, abiv1.ORMSearchReadInput{Model: "testmodule.contact", Domain: source})
		if hostErr != nil || len(out.Records) != 0 {
			t.Fatalf("archived filter = %+v, error = %+v", out, hostErr)
		}
	}
}

func TestORMMany2ManyFiltersRespectTargetRLS(t *testing.T) {
	db, r, mc := m2mFixture(t)
	id := m2mCreate(t, db, r, mc, []string{m2mTag1, m2mTag2})
	m2mCreate(t, db, r, mc, nil)
	schema := "tenant_" + mc.TenantSlug
	reader := openTestRLSReaderORM(t, db, schema, schema+".contact_tags")
	for _, statement := range []string{
		"ALTER TABLE " + schema + ".contact_tags ENABLE ROW LEVEL SECURITY",
		"CREATE POLICY visible ON " + schema + ".contact_tags FOR SELECT USING (name = 'VIP')",
		"GRANT SELECT ON ALL TABLES IN SCHEMA " + schema + " TO goerp_test_rls_reader_orm",
	} {
		if _, err := db.ExecContext(t.Context(), statement); err != nil {
			t.Fatal(err)
		}
	}

	for _, tc := range []struct {
		source string
		count  int64
	}{
		{"record.tag_ids = '" + m2mTag1 + "'", 1},
		{"record.tag_ids = '" + m2mTag2 + "'", 0},
		{"record.tag_ids IN ('" + m2mTag1 + "', '" + m2mTag2 + "')", 1},
		{"record.tag_ids != '" + m2mTag2 + "'", 2},
		{"record.tag_ids IS NULL", 1},
	} {
		out, hostErr := ORMSearch(t.Context(), reader, mc, abiv1.ORMSearchInput{Model: "testmodule.contact", Domain: tc.source})
		if hostErr != nil || out.Count != tc.count {
			t.Fatalf("%s: out = %+v, error = %+v", tc.source, out, hostErr)
		}
		if tc.count == 1 && tc.source != "record.tag_ids IS NULL" && (len(out.IDs) != 1 || out.IDs[0] != id) {
			t.Errorf("IDs = %v, want %s", out.IDs, id)
		}
	}
}
