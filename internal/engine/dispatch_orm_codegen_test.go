package engine

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/codegen"
	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/djangbahevans/goerp/internal/engine/config"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/registry"
	"github.com/djangbahevans/goerp/internal/engine/route"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
	"github.com/djangbahevans/goerp/internal/engine/wasm"
	"github.com/djangbahevans/goerp/internal/engine/wasm/wasmtest"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

// allKindsModelDecl declares one field of every field kind.
func allKindsModelDecl() (model.ModelDeclaration, []model.TypeDeclaration) {
	d := model.Define("testmodule.kinds", model.Table("kinds")).WithStandardFields().
		Field("c_char", model.Char(20)).
		Field("c_text", model.Text()).
		Field("c_int", model.Integer()).
		Field("c_bigint", model.BigInt()).
		Field("c_float", model.Float()).
		Field("c_decimal", model.Decimal(12, 2)).
		Field("c_bool", model.Boolean()).
		Field("c_uuid", model.UUID()).
		Field("c_ts", model.TimestampTZ()).
		Field("c_date", model.Date()).
		Field("c_time", model.Time()).
		Field("c_json", model.JSONB()).
		Field("c_bytea", model.Bytea()).
		Field("c_sel", model.Selection("draft", "done")).
		Field("c_enum", model.Enum("kind_size")).
		Field("c_seq", model.Sequence("K/{seq:5}")).
		Field("partner_id", model.Many2One("testmodule.kinds")).
		Field("ref_type", model.Selection("testmodule.kinds")).
		Field("ref_id", model.DynamicLink("ref_type")).
		Field("lines", model.One2Many("testmodule.kinds", "partner_id"))
	return *d, []model.TypeDeclaration{model.EnumType("kind_size", "s", "m")}
}

// TestORMResponse_MatchesCodegenFieldTypes reads records through the real
// get and list ORM routes and checks every field kind's JSON value against
// the TypeScript type goerp codegen generates for it
// (typescript-sdk-reference.md §4 "Types"): one record with every field
// set, and one with every nullable field null.
func TestORMResponse_MatchesCodegenFieldTypes(t *testing.T) {
	conn := openDispatchORMTestDB(t)
	ensureRiverJobMigrated(t)
	ctx := t.Context()
	slug := fmt.Sprintf("codegenkinds%d", time.Now().UnixNano())
	schemaName := tenantschema.Name(slug)
	if _, err := conn.ExecContext(ctx, "CREATE SCHEMA "+schemaName); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(func() { _, _ = conn.ExecContext(context.Background(), "DROP SCHEMA "+schemaName+" CASCADE") })

	for _, stmt := range []string{
		`CREATE TYPE ` + schemaName + `.kind_size AS ENUM ('s', 'm')`,
		`CREATE TABLE ` + schemaName + `.kinds (
			id UUID PRIMARY KEY DEFAULT uuidv7(),
			tenant_id UUID NOT NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			deleted_at TIMESTAMPTZ,
			created_by UUID,
			etag TEXT NOT NULL DEFAULT '',
			c_char VARCHAR(20), c_text TEXT, c_int INTEGER, c_bigint BIGINT, c_float DOUBLE PRECISION,
			c_decimal NUMERIC(12, 2), c_bool BOOLEAN, c_uuid UUID, c_ts TIMESTAMPTZ, c_date DATE, c_time TIME,
			c_json JSONB, c_bytea BYTEA, c_sel TEXT, c_enum ` + schemaName + `.kind_size, c_seq TEXT,
			partner_id UUID, ref_type TEXT, ref_id UUID
		)`,
	} {
		if _, err := conn.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("create fixture: %v", err)
		}
	}

	tenantID := "00000000-0000-0000-0000-000000000001"
	const fullID, emptyID = "0192f5d6-0000-7000-8000-000000000001", "0192f5d6-0000-7000-8000-000000000002"
	if _, err := conn.ExecContext(ctx, `INSERT INTO `+schemaName+`.kinds
		(id, tenant_id, c_char, c_text, c_int, c_bigint, c_float, c_decimal, c_bool, c_uuid, c_ts, c_date, c_time,
		 c_json, c_bytea, c_sel, c_enum, c_seq, partner_id, ref_type, ref_id)
		VALUES ($1, $2, 'ab', 'long text', 42, 9007199254740993, 1.5, 123.45, true, gen_random_uuid(),
		 '2024-03-15T10:30:00+02:00', '2024-03-15', '10:30:00', '{"k": [1, "two"]}', '\x6869', 'done', 'm',
		 'K/00001', $1, 'testmodule.kinds', $1)`, fullID, tenantID); err != nil {
		t.Fatalf("insert full record: %v", err)
	}
	if _, err := conn.ExecContext(ctx, `INSERT INTO `+schemaName+`.kinds (id, tenant_id) VALUES ($1, $2)`, emptyID, tenantID); err != nil {
		t.Fatalf("insert empty record: %v", err)
	}

	rt, err := wasm.New(&config.Config{
		CompilationCache:            wasmtest.SharedCompilationCacheDir(),
		Environment:                 string(config.Production),
		PoolMaxMemoryByes:           1 << 20,
		DBMaxConcurrentTransactions: 10,
	}, conn, nil, nil)
	if err != nil {
		t.Fatalf("wasm.New: %v", err)
	}
	t.Cleanup(func() { _ = rt.Close(context.Background()) })

	md, types := allKindsModelDecl()
	reg := &registry.ModuleRegistry{}
	if _, err := reg.Update(map[string]*module.LoadedModule{
		"testmodule": {
			Status:       module.StatusReady,
			Manifest:     manifest.Manifest{Name: "testmodule", Type: "standard"},
			ModelDecls:   []model.ModelDeclaration{md},
			TypeDecls:    types,
			Capabilities: abi.CapDBRead | abi.CapDBWrite,
		},
	}); err != nil {
		t.Fatalf("registry Update: %v", err)
	}
	entry := func(action, path string) *route.RouteEntry {
		return &route.RouteEntry{ModuleName: "testmodule", PathTemplate: path, Manifest: route.RouteManifest{
			Auth: "required", Model: "testmodule.kinds", CrudAction: action, ResponseIsList: action == "list",
			EngineNative: true, StorageBackend: "table",
		}}
	}
	f := &dispatchORMFixture{
		e:        &Engine{primaryDB: conn, wasmRuntime: rt, moduleRegistry: reg},
		slug:     slug,
		tenantID: tenantID,
		entryGet: entry("get", "/testmodule/kinds/{id}"),
	}
	f.entryList = entry("list", "/testmodule/kinds")

	// The generated types are what codegen builds from the same
	// declaration, as it appears in /_meta/schema.
	in, err := codegen.InputFromSchema(schemaResponseJSON(t, reg), "testmodule")
	if err != nil {
		t.Fatalf("InputFromSchema: %v", err)
	}
	if len(in.Models) != 1 || len(in.Models[0].Fields) != len(md.Fields) {
		t.Fatalf("codegen input = %+v, want the one kinds model with all %d fields", in.Models, len(md.Fields))
	}
	fields := in.Models[0].Fields

	get := func(id string) map[string]any {
		w := httptest.NewRecorder()
		f.e.dispatchORMRoute(w, f.request(http.MethodGet, "/testmodule/kinds/"+id, nil, f.entryGet, map[string]string{"id": id}))
		if w.Code != http.StatusOK {
			t.Fatalf("get %s: status %d, body %s", id, w.Code, w.Body.String())
		}
		var rec map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &rec); err != nil {
			t.Fatalf("decode get response: %v", err)
		}
		return rec
	}

	w := httptest.NewRecorder()
	f.e.dispatchORMRoute(w, f.request(http.MethodGet, "/testmodule/kinds", nil, f.entryList, nil))
	var list struct {
		Data []map[string]any `json:"data"`
	}
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &list) != nil || len(list.Data) != 2 {
		t.Fatalf("list: status %d, body %s", w.Code, w.Body.String())
	}

	records := map[string]map[string]any{"full (get)": get(fullID), "empty (get)": get(emptyID)}
	for _, rec := range list.Data {
		records[fmt.Sprintf("%v (list)", rec["id"])] = rec
	}

	for label, rec := range records {
		for _, field := range fields {
			ts, ok := codegen.FieldTSType(field.Kind, field.Values)
			value, present := rec[field.Name]
			if !ok {
				if present {
					t.Errorf("%s: %s (%s) is on the record, but codegen leaves it out of the type", label, field.Name, field.Kind)
				}
				continue
			}
			if !present {
				t.Errorf("%s: %s (%s) is missing from the record, but the type declares it", label, field.Name, field.Kind)
				continue
			}
			if value == nil {
				if field.Required || field.PrimaryKey {
					t.Errorf("%s: %s is null, but its type is non-nullable %s", label, field.Name, ts)
				}
				continue
			}
			if !jsonMatchesTS(value, ts, field.Values) {
				t.Errorf("%s: %s (%s) = %#v, which is not a %s", label, field.Name, field.Kind, value, ts)
			}
		}
	}

	full := records["full (get)"]
	for name, want := range map[string]any{
		"c_decimal": "123.45",
		"c_date":    "2024-03-15",
		"c_time":    "10:30:00",
		"c_ts":      "2024-03-15T08:30:00Z",
		"c_bytea":   "aGk=",
		"c_seq":     "K/00001",
		"c_enum":    "m",
	} {
		if full[name] != want {
			t.Errorf("full record %s = %#v, want %#v", name, full[name], want)
		}
	}
	if _, isObject := full["c_json"].(map[string]any); !isObject {
		t.Errorf("full record c_json = %#v, want the stored JSON object", full["c_json"])
	}
}

// jsonMatchesTS reports whether a decoded JSON value inhabits the
// TypeScript type ts, a primitive or a union of the given string values.
func jsonMatchesTS(v any, ts string, values []string) bool {
	switch ts {
	case "string":
		_, ok := v.(string)
		return ok
	case "number":
		_, ok := v.(float64)
		return ok
	case "boolean":
		_, ok := v.(bool)
		return ok
	case "unknown":
		return true
	}
	s, ok := v.(string)
	return ok && slices.Contains(values, s)
}

// schemaResponseJSON is reg's /_meta/schema response body.
func schemaResponseJSON(t *testing.T, reg *registry.ModuleRegistry) []byte {
	t.Helper()
	out, err := json.Marshal(reg.Snapshot().SchemaResponse())
	if err != nil {
		t.Fatalf("marshal schema response: %v", err)
	}
	return out
}
