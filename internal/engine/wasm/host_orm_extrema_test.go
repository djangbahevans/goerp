package wasm

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/djangbahevans/goerp/internal/engine/fieldsec"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/permission"
	"github.com/djangbahevans/goerp/sdk/go/model"
	"github.com/djangbahevans/goerp/sdk/go/perm"
	"github.com/vmihailenco/msgpack/v5"
)

func extremaModelDecl() model.ModelDeclaration {
	return *model.Define("extremum", model.Table("extrema")).
		Field("id", model.UUID().PrimaryKey()).
		Field("region", model.Text()).
		Field("owner", model.Text()).
		Field("deleted_at", model.TimestampTZ()).
		Field("integer", model.Integer()).
		Field("bigint", model.BigInt()).
		Field("float", model.Float()).
		Field("decimal", model.Decimal(30, 6)).
		Field("char", model.Char(32)).
		Field("text", model.Text()).
		Field("selection", model.Selection("alpha", "zeta", "")).
		Field("enum", model.Enum("extremum_status")).
		Field("date", model.Date()).
		Field("timestamp", model.TimestampTZ()).
		Field("restricted", model.Text().Access(model.AccessRead(perm.Ref("testmodule:extremum:secret")))).
		Field("boolean", model.Boolean()).
		Field("json", model.JSONB()).
		Field("bytes", model.Bytea()).
		Field("uuid", model.UUID()).
		Field("time", model.Time())
}

func setupExtremaTenant(t *testing.T, db *sql.DB) string {
	t.Helper()

	slug := fmt.Sprintf("extrema%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, db, slug)
	schema := quoteIdentORM("tenant_" + slug)
	statements := []string{
		`CREATE TYPE ` + schema + `.extremum_status AS ENUM ('zeta', 'alpha', '')`,
		`CREATE TABLE ` + schema + `.extrema (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(), region TEXT, owner TEXT DEFAULT 'contact-1', deleted_at TIMESTAMPTZ,
			integer INTEGER, bigint BIGINT, float DOUBLE PRECISION, decimal NUMERIC(30,6),
			char VARCHAR(32), text TEXT, selection TEXT, enum ` + schema + `.extremum_status,
			date DATE, timestamp TIMESTAMPTZ, restricted TEXT,
			boolean BOOLEAN, json JSONB, bytes BYTEA, uuid UUID, time TIME
		)`,
		`INSERT INTO ` + schema + `.extrema (region, integer, bigint, float, decimal, char, text, selection, enum, date, timestamp) VALUES
			('values', -20, 9007199254740993, -2.5, 9007199254740993.123456, 'alpha', 'alpha', 'alpha', 'alpha', '2026-10-09', '2026-10-09T00:00:00.123456Z'),
			('values', 40, 9007199254740995, 4.5, 9007199254740995.654321, 'zeta', 'zeta', 'zeta', 'zeta', '2026-10-11', '2026-10-11T16:30:00.123456Z'),
			('nulls', NULL, NULL, NULL, NULL, NULL, NULL, NULL, NULL, NULL, NULL),
			('zero', 0, 0, 0, 0, '', '', '', '', '0001-01-01', '0001-01-01T00:00:00Z')`,
		`INSERT INTO ` + schema + `.extrema (region, owner, integer, bigint, float, decimal, char, text, selection, enum, date, timestamp)
			SELECT region, 'other-contact', 999, 9999999999999999, 999, 9999999999999999, 'zzzz', 'zzzz', 'zzzz', 'zeta', '2099-01-01', '2099-01-01' FROM ` + schema + `.extrema WHERE region = 'values' LIMIT 1`,
		`INSERT INTO ` + schema + `.extrema (region, deleted_at, integer, bigint, float, decimal, char, text, selection, enum, date, timestamp)
			SELECT region, NOW(), -999, -9999999999999999, -999, -9999999999999999, 'aaaa', 'aaaa', 'aaaa', 'zeta', '1900-01-01', '1900-01-01' FROM ` + schema + `.extrema WHERE region = 'values' LIMIT 1`,
		`ALTER TABLE ` + schema + `.extrema ENABLE ROW LEVEL SECURITY`,
		`CREATE POLICY extrema_owner ON ` + schema + `.extrema USING (owner = current_setting('app.current_user_contact_id', true))`,
	}
	for _, statement := range statements {
		if _, err := db.ExecContext(t.Context(), statement); err != nil {
			t.Fatalf("prepare extrema fixture: %v", err)
		}
	}

	grantFixtureTables(t, db, slug, "extrema")

	return slug
}

func extremaModuleContext(slug string) *ModuleContext {
	decl := extremaModelDecl()
	security := fieldsec.New()
	security.Register("testmodule", []model.ModelDeclaration{decl})
	permissions := permission.NewPermissionRegistry()
	permissions.Register("testmodule", []manifest.Permission{{Name: "testmodule:extremum:secret"}})

	return NewModuleContext("req-1", "testmodule", "user-1", "contact-1", nil, nil,
		"tenant-id-1", slug, "trace-1", abi.CapDBRead, nil, ModuleSnapshot{
			ModelDecls:         []model.ModelDeclaration{decl},
			FieldSecRegistry:   security,
			PermissionRegistry: permissions,
		})
}

func TestOrmCallerFixture_ExtremaPreserveTypesAndVisibility(t *testing.T) {
	db := openTestPrimaryDB(t)
	slug := setupExtremaTenant(t, db)
	otherSlug := setupExtremaTenant(t, db)
	if _, err := db.ExecContext(t.Context(), `UPDATE `+quoteIdentORM("tenant_"+otherSlug)+`.extrema SET integer = -2000`); err != nil {
		t.Fatalf("seed other tenant extrema: %v", err)
	}

	reader := openTestRLSReaderORM(t, db, "tenant_"+slug, "tenant_"+slug+".extrema")
	runtime := newHostcallTestRuntime(t, reader, 10)
	compiled, err := runtime.wazero.CompileModule(t.Context(), compileOrmCallerFixture(t))
	if err != nil {
		t.Fatalf("compile extrema caller: %v", err)
	}
	t.Cleanup(func() { _ = compiled.Close(context.Background()) })

	instance, err := newModuleInstance(t.Context(), "extrema-caller", compiled, runtime)
	if err != nil {
		t.Fatalf("instantiate extrema caller: %v", err)
	}
	instance.SetModuleContext(extremaModuleContext(slug))
	runtime.RegisterInstance(instance)
	t.Cleanup(func() { runtime.UnregisterInstance(instance) })

	results, err := instance.module.ExportedFunction("run_extrema").Call(t.Context())
	if err != nil {
		t.Fatalf("invoke extrema caller: %v", err)
	}
	raw, ok := instance.module.Memory().Read(uint32(results[0]>>32), uint32(results[0]))
	if !ok {
		t.Fatal("extrema report exceeds module memory")
	}
	var report ormFlowReport
	if err := msgpack.Unmarshal(raw, &report); err != nil {
		t.Fatalf("decode extrema report: %v", err)
	}

	values := map[string][2]string{
		"integer":   {"-20", "40"},
		"bigint":    {"9007199254740993", "9007199254740995"},
		"float":     {"-2.5", "4.5"},
		"decimal":   {"9007199254740993.123456", "9007199254740995.654321"},
		"char":      {"alpha", "zeta"},
		"text":      {"alpha", "zeta"},
		"selection": {"alpha", "zeta"},
		"enum":      {"zeta", "alpha"},
		"date":      {"2026-10-09 00:00:00 +0000 UTC", "2026-10-11 00:00:00 +0000 UTC"},
		"timestamp": {"2026-10-09 00:00:00.123456 +0000 UTC", "2026-10-11 16:30:00.123456 +0000 UTC"},
	}
	zero := map[string]string{
		"integer": "0", "bigint": "0", "float": "0", "decimal": "0.000000",
		"char": "", "text": "", "selection": "", "enum": "",
		"date": "0001-01-01 00:00:00 +0000 UTC", "timestamp": "0001-01-01 00:00:00 +0000 UTC",
	}
	want := map[string]string{
		"named_type": "", "time_type": "", "min_read_denied": "", "max_read_denied": "",
		"values_count": "2", "values_sum": "20", "values_avg": "10",
		"empty_count": "0", "empty_sum": "0", "empty_avg": "0",
		"nulls_count": "1", "nulls_sum": "0", "nulls_avg": "0",
		"zero_count": "1", "zero_sum": "0", "zero_avg": "0",
	}
	for field, pair := range values {
		for i, operation := range []string{"min", "max"} {
			want["values_"+field+"_"+operation] = pair[i]
			want["empty_"+field+"_"+operation] = "null"
			want["nulls_"+field+"_"+operation] = "null"
			want["zero_"+field+"_"+operation] = zero[field]
		}
	}
	for _, step := range report.Steps {
		expected, exists := want[step.Step]
		if !exists {
			t.Errorf("unexpected extrema step %s", step.Step)
			continue
		}
		if !step.OK || step.Detail != expected {
			t.Errorf("%s: detail %q, error %q; want %q", step.Step, step.Detail, step.Error, expected)
		}
		delete(want, step.Step)
	}
	if len(want) != 0 {
		t.Errorf("missing extrema steps: %v", want)
	}
}

func TestORMAggregate_ExtremaValidationAndReadDenial(t *testing.T) {
	db := openTestPrimaryDB(t)
	mc := extremaModuleContext(setupExtremaTenant(t, db))

	for _, operation := range []string{"min", "max"} {
		for _, field := range []string{"restricted", "boolean", "json", "bytes", "uuid", "time"} {
			t.Run(operation+"_"+field, func(t *testing.T) {
				_, hostErr := ORMAggregate(t.Context(), db, mc, abiv1.ORMAggregateInput{
					Model:  "testmodule.extremum",
					Values: []abiv1.ORMAggregateValue{{Field: field, Aggregation: operation}},
				})
				want := abiv1.ErrCodeValidationFailed
				if field == "restricted" {
					want = abiv1.ErrCodeFieldReadDenied
				}

				if hostErr == nil || hostErr.Code != want || hostErr.Details["field"] != field {
					t.Fatalf("host error = %+v, want %s for %s", hostErr, want, field)
				}
			})
		}
	}

	for _, operation := range []string{"sum", "avg"} {
		for _, field := range []string{"text", "date", "selection", "enum"} {
			t.Run(operation+"_"+field, func(t *testing.T) {
				_, hostErr := ORMAggregate(t.Context(), db, mc, abiv1.ORMAggregateInput{
					Model:  "testmodule.extremum",
					Values: []abiv1.ORMAggregateValue{{Field: field, Aggregation: operation}},
				})
				if hostErr == nil || hostErr.Code != abiv1.ErrCodeValidationFailed {
					t.Fatalf("host error = %+v, want numeric validation failure", hostErr)
				}
			})
		}
	}
}
