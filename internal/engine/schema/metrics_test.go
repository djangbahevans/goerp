package schema

import (
	"strings"
	"testing"

	"ariga.io/atlas/sql/schema"
	"github.com/djangbahevans/goerp/internal/engine/metric"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestSchemaOperationsExcludeRolledBackDDL(t *testing.T) {
	sess, engine := setupTenantSchema(t, "metrics_rollback")
	reg := prometheus.NewRegistry()
	engine.cfg.Metrics = metric.New(reg)
	table := schema.NewTable("metrics_rollback").AddColumns(schema.NewIntColumn("id", "integer"))
	changes := []schema.Change{&schema.AddTable{T: table}, &schema.AddTable{T: table}}
	if _, _, err := engine.ExecuteAccepted(t.Context(), sess, nil, changes, nil); err == nil {
		t.Fatal("duplicate table DDL succeeded")
	}
	if err := testutil.GatherAndCompare(reg, strings.NewReader(""), "erp_schema_sync_operations_total"); err != nil {
		t.Fatal(err)
	}
}
