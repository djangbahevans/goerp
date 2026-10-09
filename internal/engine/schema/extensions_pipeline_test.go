package schema

import (
	"context"
	"crypto/sha256"
	"encoding/json/v2"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
	"uuid"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/codegen"
	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/djangbahevans/goerp/internal/engine/loader"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/permission"
	"github.com/djangbahevans/goerp/internal/engine/registry"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
	"github.com/djangbahevans/goerp/internal/engine/wasm"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

func extensionSource(t *testing.T, fixture, name, moduleType string, schema, extra map[string]any) loader.Source {
	t.Helper()

	path := filepath.Join(t.TempDir(), fixture+".wasm")
	cmd := exec.CommandContext(t.Context(), "go", "build", "-buildmode=c-shared", "-o", path, "./testdata/"+fixture)
	cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("compile %s: %v\n%s", fixture, err, output)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	manifest := map[string]any{
		"name":         name,
		"display_name": name,
		"type":         moduleType,
		"version":      "1.0.0",
		"description":  "Model extension integration fixture",
		"abi_version":  "1",
		"engine":       ">=0.5.0 <1.0.0",
		"depends_on":   []string{},
		"capabilities": []string{"db.read", "db.write"},
		"schema":       schema,
		"checksum":     fmt.Sprintf("sha256:%x", sha256.Sum256(data)),
	}
	maps.Copy(manifest, extra)
	encoded, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	return loader.Source{Name: name, ManifestBytes: encoded, WasmBytes: data}
}

func TestModelExtensionPipeline(t *testing.T) {
	baseSource := extensionSource(t, "extensionbase", "contacts", "domain", map[string]any{
		"owned_models": []string{"contacts.contact"},
	}, nil)
	extSource := extensionSource(t, "extensionfields", "industry", "field_extension", map[string]any{
		"owned_models":   []string{},
		"extends_module": "contacts",
		"extends_models": []string{"contacts.contact"},
	}, map[string]any{
		"depends_on": []string{"contacts"},
		"permissions": []map[string]any{
			{"name": "industry:contact:read", "description": "Read industry"},
			{"name": "industry:contact:write", "description": "Write industry"},
		},
		"view_extension_definitions": []map[string]any{{
			"name": "industry_column", "type": "columns", "target_section": "columns", "position": "append",
			"columns": []map[string]any{{"field": "industry"}},
		}},
	})
	rt := newEnumFixtureRuntime(t)
	mods := loader.LoadAll(t.Context(), rt, wasm.PoolConfig{MaxSize: 2, BorrowTimeout: time.Second}, []loader.Source{baseSource, extSource})
	for name, mod := range mods {
		if mod.Status == module.StatusFailed {
			t.Fatalf("load %s: %s", name, mod.FailureReason)
		}
		t.Cleanup(func() { mod.Pool.DrainAndClose(context.Background(), 5*time.Second) })
	}
	t.Run("undeclared target fails compiled module load", func(t *testing.T) {
		var bad map[string]any
		if err := json.Unmarshal(extSource.ManifestBytes, &bad); err != nil {
			t.Fatal(err)
		}
		bad["schema"].(map[string]any)["extends_models"] = []string{"contacts.missing"}
		encoded, err := json.Marshal(bad)
		if err != nil {
			t.Fatal(err)
		}
		source := extSource
		source.ManifestBytes = encoded
		loaded := loader.LoadModule(t.Context(), rt, wasm.PoolConfig{MaxSize: 1, BorrowTimeout: time.Second}, source)
		if loaded.Status != module.StatusFailed || !strings.Contains(loaded.FailureReason, "contacts.contact") {
			t.Fatalf("invalid extension load = %v: %s", loaded.Status, loaded.FailureReason)
		}
	})

	base, ext := mods["contacts"], mods["industry"]
	if len(ext.ModelExtensions) != 2 || len(ext.ModelDecls) != 0 {
		t.Fatalf("extension declarations = %+v", ext)
	}

	reg := &registry.ModuleRegistry{}
	snap, err := reg.Update(mods)
	if err != nil {
		t.Fatal(err)
	}
	if len(base.ModelDecls[0].Fields) != 9 {
		t.Fatal("registry mutated base declarations")
	}
	if _, exists := snap.SchemaResponse().Modules["industry"].Models["contacts.contact"]; exists {
		t.Fatal("extension creates a duplicate schema model")
	}
	modelSchema := snap.SchemaResponse().Modules["contacts"].Models["contacts.contact"]
	if len(modelSchema.Fields) != 15 {
		t.Fatalf("merged schema fields = %+v", modelSchema.Fields)
	}

	encoded, err := json.Marshal(snap.SchemaResponse())
	if err != nil {
		t.Fatal(err)
	}
	input, err := codegen.InputFromSchema(encoded, "contacts")
	if err != nil {
		t.Fatal(err)
	}
	client, err := codegen.Generate(input)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(client), "industry: string | null;") {
		t.Fatalf("generated Contact lacks industry:\n%s", client)
	}
	if !strings.Contains(string(client), "category: 'retail' | 'service' | null;") {
		t.Fatalf("generated Contact lacks extension enum values:\n%s", client)
	}

	conn, pool := openTestPool(t, 5*time.Second)
	slug := fmt.Sprintf("modelext%d", time.Now().UnixNano())
	tenantID := uuid.NewV7().String()
	if err := tenantschema.Create(t.Context(), conn, slug); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = conn.Exec("DELETE FROM system.model_extension_objects WHERE tenant_id = $1", tenantID)
		_, _ = conn.Exec("DELETE FROM system.pending_constraint_validations WHERE tenant_id = $1", tenantID)
		_, _ = conn.Exec("DELETE FROM system.river_job WHERE args->>'tenant_id' = $1", tenantID)
		if err := tenantschema.Drop(context.Background(), conn, slug); err != nil {
			t.Errorf("drop tenant: %v", err)
		}
	})
	diffEngine := NewSchemaDiffEngine(&Config{ModelSource: reg})
	syncModule := func(mod *module.LoadedModule) {
		t.Helper()
		sess, err := pool.BeginSync(t.Context(), tenantID, slug, mod.Manifest.Name, &mod.Manifest)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = sess.Close(t.Context()) }()

		changes, err := diffEngine.Diff(t.Context(), sess, mod.ModelDecls, mod.TypeDecls, mod.ModelExtensions...)
		if err != nil {
			t.Fatal(err)
		}
		blocked, _, err := diffEngine.ExecuteAccepted(t.Context(), sess, mod.ModelDecls, changes, nil)
		if err != nil || len(blocked) > 0 {
			t.Fatalf("execute schema: blocked=%v err=%v", blocked, err)
		}
		if err := diffEngine.SyncTenantRoleGrants(t.Context(), sess, mod.ModelDecls); err != nil {
			t.Fatal(err)
		}
	}
	syncModule(base)
	syncModule(ext)
	syncModule(ext)
	syncModule(base)
	t.Run("existing extension definitions cannot change", func(t *testing.T) {
		sess, err := pool.BeginRead(t.Context(), tenantID, slug, ext.Manifest.Name, &ext.Manifest)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = sess.Close(t.Context()) }()

		changed := ext.ModelExtensions[0]
		changed.Fields = slices.Clone(changed.Fields)
		changed.Fields[0].Def = model.Char(120)
		_, err = diffEngine.Diff(t.Context(), sess, nil, ext.TypeDecls, changed)
		if err == nil || !strings.Contains(err.Error(), "only permits additions") {
			t.Fatalf("column alteration = %v", err)
		}

		changed = ext.ModelExtensions[1]
		changed.Indexes = slices.Clone(changed.Indexes)
		changed.Indexes[0].Def = changed.Indexes[0].Def.Unique()
		_, err = diffEngine.Diff(t.Context(), sess, nil, ext.TypeDecls, changed)
		if err == nil || !strings.Contains(err.Error(), "only permits additions") {
			t.Fatalf("index alteration = %v", err)
		}

		invalid := model.Extend("contacts.contact").Field("children", model.One2Many("contacts.contact", "missing_inverse"))
		_, err = diffEngine.Diff(t.Context(), sess, nil, ext.TypeDecls, invalid)
		if err == nil || !strings.Contains(err.Error(), "inverse field") {
			t.Fatalf("invalid inverse relation = %v", err)
		}
	})

	if !columnExists(t, conn, "tenant_"+slug, "contacts", "industry") ||
		!indexExists(t, conn, "tenant_"+slug, "contacts_industry_idx") ||
		!indexExists(t, conn, "tenant_"+slug, "contacts_name_idx") {
		t.Fatal("base sync did not preserve extension columns and indexes")
	}

	newContext := func(moduleName string, read, write bool) *wasm.ModuleContext {
		var granted permission.PermissionBitfield
		for name, enabled := range map[string]bool{"industry:contact:read": read, "industry:contact:write": write} {
			if enabled {
				idx, _ := snap.PermissionRegistry().Index(name)
				granted.Set(idx)
			}
		}
		decls, _ := snap.ModelDeclarations(moduleName)
		return wasm.NewModuleContext("extension-test", moduleName, uuid.NewV7().String(), "", []string{"admin"}, granted,
			tenantID, slug, "extension-trace", abi.CapDBRead|abi.CapDBWrite, nil, wasm.ModuleSnapshot{
				ModelDecls:         decls,
				ComputeTargets:     registry.ComputeTargets(snap),
				FieldSecRegistry:   snap.FieldSecRegistry(),
				PermissionRegistry: snap.PermissionRegistry(),
				ComputedIndex:      snap.ComputedIndex(),
			})
	}
	created, hostErr := wasm.ORMCreate(t.Context(), rt, conn, rt.EventInsertClient(), nil, newContext("contacts", true, true), abiv1.ORMCreateInput{
		Model: "contacts.contact", Record: map[string]any{"name": "A", "industry": "Retail"},
	})
	if hostErr != nil || created.Record["industry"] != "Retail" {
		t.Fatalf("create extension field: %+v, %+v", created, hostErr)
	}
	if fmt.Sprint(created.Record["score"]) != "42" {
		t.Fatalf("extension-owned compute result = %v", created.Record["score"])
	}
	id := fmt.Sprint(created.Record["id"])
	for _, caller := range []string{"contacts", "industry", "another"} {
		for _, permitted := range []bool{false, true} {
			out, hostErr := wasm.ORMSearchRead(t.Context(), conn, newContext(caller, permitted, false), abiv1.ORMSearchReadInput{Model: "contacts.contact"})
			if hostErr != nil || len(out.Records) != 1 {
				t.Fatalf("%s search_read: %+v, %+v", caller, out, hostErr)
			}
			value, present := out.Records[0]["industry"]
			if present != permitted || permitted && value != "Retail" {
				t.Fatalf("%s read permission=%v industry=%v present=%v", caller, permitted, value, present)
			}
		}
	}
	_, hostErr = wasm.ORMWrite(t.Context(), rt, conn, rt.EventInsertClient(), nil, newContext("contacts", true, false), abiv1.ORMWriteInput{
		Model: "contacts.contact", ID: id, Record: map[string]any{"industry": "Other"},
	})
	if hostErr == nil || hostErr.Code != abiv1.ErrCodeFieldWriteDenied {
		t.Fatalf("denied extension write: %+v", hostErr)
	}
	updated, hostErr := wasm.ORMWrite(t.Context(), rt, conn, rt.EventInsertClient(), nil, newContext("contacts", false, true), abiv1.ORMWriteInput{
		Model: "contacts.contact", ID: id, Record: map[string]any{"industry": "Other"},
	})
	if hostErr != nil {
		t.Fatalf("allowed extension write: %+v", hostErr)
	}
	if _, exposed := updated.Record["industry"]; exposed {
		t.Fatal("write response exposes unreadable extension field")
	}

	if _, err := reg.Update(map[string]*module.LoadedModule{"contacts": base}); err != nil {
		t.Fatal(err)
	}
	syncModule(base)
	if !indexExists(t, conn, "tenant_"+slug, "contacts_name_idx") {
		t.Fatal("unloading extension loses extension index on base fields")
	}
	if snap.SchemaHash() == reg.Snapshot().SchemaHash() {
		t.Fatal("extension removal does not invalidate the schema hash")
	}
	if len(snap.SchemaResponse().Modules["contacts"].Models["contacts.contact"].Fields) != 15 {
		t.Fatal("extension removal mutates an existing snapshot")
	}
	snap = reg.Snapshot()
	created, hostErr = wasm.ORMCreate(t.Context(), rt, conn, rt.EventInsertClient(), nil, newContext("contacts", false, false), abiv1.ORMCreateInput{
		Model: "contacts.contact", Record: map[string]any{"name": "B"},
	})
	if hostErr != nil {
		t.Fatalf("create after extension removal: %+v", hostErr)
	}
	if _, exposed := created.Record["industry"]; exposed {
		t.Fatal("write response includes an orphaned extension column")
	}
}
