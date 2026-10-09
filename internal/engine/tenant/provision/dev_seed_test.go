package tenantprovision

import (
	"encoding/json/jsontext"
	"testing"

	"github.com/djangbahevans/goerp/internal/engine/adminapi"
	"github.com/djangbahevans/goerp/internal/engine/module"
)

func TestDevSeedCommitsAllRowsOrRollsBack(t *testing.T) {
	d, conn := devFixture(t)
	session, err := d.BootstrapDev(t.Context(), "sales")
	if err != nil {
		t.Fatal(err)
	}

	mod := *d.activities.registry.Snapshot().Modules()["sales"]
	foreign := widgetModel()
	foreign.Name = "other.widget"
	mod.ModelDecls = append(mod.ModelDecls, foreign)
	if _, err := d.activities.registry.Update(map[string]*module.LoadedModule{"sales": new(mod)}); err != nil {
		t.Fatal(err)
	}

	record := func(name string) map[string]jsontext.Value {
		return map[string]jsontext.Value{"name": jsontext.Value(`"` + name + `"`)}
	}
	batches := []adminapi.DevSeedBatch{
		{Model: "sales.widget", Records: []map[string]jsontext.Value{record("first"), record("second")}},
	}
	if err := d.SeedDev(t.Context(), "sales", batches); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := conn.QueryRowContext(t.Context(), "SELECT count(*) FROM tenant_dev.widgets WHERE tenant_id = $1", session.TenantID).Scan(&count); err != nil || count != 2 {
		t.Fatalf("committed seed rows: %d, %v", count, err)
	}

	batches[0].Records = []map[string]jsontext.Value{record("rolled-back"), {"name": jsontext.Value(`null`)}}
	if err := d.SeedDev(t.Context(), "sales", batches); err == nil {
		t.Fatal("seed violating a required field succeeded")
	}
	if err := conn.QueryRowContext(t.Context(), "SELECT count(*) FROM tenant_dev.widgets").Scan(&count); err != nil || count != 2 {
		t.Fatalf("failed seed changed tenant data: %d, %v", count, err)
	}

	for _, batch := range []adminapi.DevSeedBatch{
		{Model: "other.widget", Records: []map[string]jsontext.Value{record("foreign")}},
		{Model: "sales.widget", Records: []map[string]jsontext.Value{{"tenant_id": jsontext.Value(`"other"`)}}},
		{Model: "sales.widget", Records: []map[string]jsontext.Value{{"unknown": jsontext.Value(`1`)}}},
	} {
		if err := d.SeedDev(t.Context(), "sales", []adminapi.DevSeedBatch{batch}); err == nil {
			t.Fatalf("invalid seed accepted: %+v", batch)
		}
	}
}
