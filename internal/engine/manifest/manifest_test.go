package manifest

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// minimalManifestFields returns a JSON-object-shaped map with exactly the
// required root fields (manifest-spec.md §2) populated with valid values,
// and no optional fields set.
func minimalManifestFields() map[string]any {
	return map[string]any{
		"name":         "demo",
		"display_name": "Demo",
		"type":         "domain",
		"version":      "1.0.0",
		"description":  "A demo module",
		"abi_version":  "1",
		"engine":       ">=0.5.0 <1.0.0",
		"depends_on":   []string{},
		"capabilities": []string{"db.read"},
		"schema": map[string]any{
			"owned_models": []string{"sales.order"},
		},
		"checksum": "sha256:empty",
	}
}

func TestLoadManifestValidMinimal(t *testing.T) {
	m, err := json.Marshal(minimalManifestFields())
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}

	if _, err := Load(m); err != nil {
		t.Fatalf("expected valid minimal manifest to pass, got %v", err)
	}
}

func TestLoadManifestRejectsMissingRequiredFields(t *testing.T) {
	// jsonField -> the Go struct field name the validator error must
	// reference, so each missing field produces a distinct, identifiable
	// error rather than an interchangeable generic one.
	requiredFields := map[string]string{
		"name":         "Name",
		"display_name": "DisplayName",
		"type":         "Type",
		"version":      "Version",
		"description":  "Description",
		"abi_version":  "ABIVersion",
		"engine":       "Engine",
		"depends_on":   "DependsOn",
		"capabilities": "Capabilities",
		"schema":       "Schema",
		"checksum":     "Checksum",
	}

	for jsonField, goField := range requiredFields {
		t.Run(jsonField, func(t *testing.T) {
			fields := minimalManifestFields()
			delete(fields, jsonField)

			m, err := json.Marshal(fields)
			if err != nil {
				t.Fatalf("marshal fixture: %v", err)
			}

			_, err = Load(m)
			if err == nil {
				t.Fatalf("expected missing %q to be rejected", jsonField)
			}
			if !strings.Contains(err.Error(), goField) {
				t.Fatalf("expected error for missing %q to reference field %q, got %v", jsonField, goField, err)
			}
		})
	}
}

func TestLoadManifestWorkerChecksumRequiredOnlyWithWorkflowTypes(t *testing.T) {
	workflowType := map[string]any{"name": "wf", "label": "WF", "handler": "h"}

	cases := map[string]struct {
		workflowTypes  []map[string]any
		workerChecksum string
		wantErr        bool
	}{
		"no workflow_types, no worker_checksum":          {nil, "", false},
		"empty workflow_types, no worker_checksum":       {[]map[string]any{}, "", false},
		"non-empty workflow_types, no worker_checksum":   {[]map[string]any{workflowType}, "", true},
		"non-empty workflow_types, with worker_checksum": {[]map[string]any{workflowType}, "sha256:deadbeef", false},
	}

	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			fields := minimalManifestFields()
			if c.workflowTypes != nil {
				fields["workflow_types"] = c.workflowTypes
			}
			if c.workerChecksum != "" {
				fields["worker_checksum"] = c.workerChecksum
			}

			m, err := json.Marshal(fields)
			if err != nil {
				t.Fatalf("marshal fixture: %v", err)
			}

			_, err = Load(m)
			if (err != nil) != c.wantErr {
				t.Fatalf("wantErr=%v, got err=%v", c.wantErr, err)
			}
		})
	}
}

func TestLoadManifestEmitsNameConvention(t *testing.T) {
	cases := map[string]struct {
		name    string
		wantErr bool
	}{
		"valid three-segment name": {"demo.order.created", false},
		"uppercase rejected":       {"Demo.order.created", true},
		"two segments rejected":    {"demo.created", true},
		"four segments rejected":   {"demo.sales.order.created", true},
		"leading digit segment":    {"demo.1order.created", true},
		"too long":                 {"demo." + strings.Repeat("a", 120) + ".created", true},
	}

	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			fields := minimalManifestFields()
			fields["capabilities"] = []string{"db.read", "event.emit"}
			fields["emits"] = []map[string]any{{"name": c.name}}

			m, err := json.Marshal(fields)
			if err != nil {
				t.Fatalf("marshal fixture: %v", err)
			}

			_, err = Load(m)
			if (err != nil) != c.wantErr {
				t.Fatalf("event name %q: wantErr=%v, got err=%v", c.name, c.wantErr, err)
			}
		})
	}
}

func TestLoadManifestHTTPFetchRequiresNonEmptyAllowlist(t *testing.T) {
	cases := map[string]struct {
		capabilities  []string
		httpAllowlist []string
		wantErr       bool
	}{
		"http.fetch without allowlist":        {[]string{"http.fetch"}, nil, true},
		"http.fetch with empty allowlist":     {[]string{"http.fetch"}, []string{}, true},
		"http.fetch with non-empty allowlist": {[]string{"http.fetch"}, []string{"example.com"}, false},
		"no http.fetch, no allowlist":         {[]string{"db.read"}, nil, false},
	}

	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			fields := minimalManifestFields()
			fields["capabilities"] = c.capabilities
			if c.httpAllowlist != nil {
				fields["http_allowlist"] = c.httpAllowlist
			}

			m, err := json.Marshal(fields)
			if err != nil {
				t.Fatalf("marshal fixture: %v", err)
			}

			_, err = Load(m)
			if (err != nil) != c.wantErr {
				t.Fatalf("wantErr=%v, got err=%v", c.wantErr, err)
			}
			if c.wantErr && !strings.Contains(err.Error(), "HTTPAllowlist") {
				t.Fatalf("expected error to reference HTTPAllowlist, got %v", err)
			}
		})
	}
}

func TestLoadManifestEmitsRequiresEventEmitCapability(t *testing.T) {
	cases := map[string]struct {
		capabilities []string
		emits        []map[string]any
		wantErr      bool
	}{
		"emits without event.emit capability": {[]string{"db.read"}, []map[string]any{{"name": "demo.order.created"}}, true},
		"emits with event.emit capability":    {[]string{"db.read", "event.emit"}, []map[string]any{{"name": "demo.order.created"}}, false},
		"no emits, no event.emit capability":  {[]string{"db.read"}, nil, false},
	}

	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			fields := minimalManifestFields()
			fields["capabilities"] = c.capabilities
			if c.emits != nil {
				fields["emits"] = c.emits
			}

			m, err := json.Marshal(fields)
			if err != nil {
				t.Fatalf("marshal fixture: %v", err)
			}

			_, err = Load(m)
			if (err != nil) != c.wantErr {
				t.Fatalf("wantErr=%v, got err=%v", c.wantErr, err)
			}
			if c.wantErr && !strings.Contains(err.Error(), "Capabilities") {
				t.Fatalf("expected error to reference Capabilities, got %v", err)
			}
		})
	}
}

// validTypeFields returns a manifest that satisfies every per-type
// constraint (manifest-spec.md §3) for moduleType — the baseline each
// negative test case in TestLoadManifestModuleTypeConstraints mutates one
// field away from.
func validTypeFields(moduleType string) map[string]any {
	fields := minimalManifestFields()
	fields["type"] = moduleType
	fields["schema"] = map[string]any{"owned_models": []string{}}

	switch moduleType {
	case "domain":
		fields["schema"] = map[string]any{"owned_models": []string{"sales.order"}}
	case "connector":
		fields["wasm"] = true
	case "bridge":
		fields["depends_on"] = []string{"mod_a", "mod_b"}
	case "theme":
		fields["wasm"] = false
		fields["views"] = []map[string]any{{"name": "settings", "type": "list", "resource": "theme.settings", "label": "Settings"}}
	case "report_bundle":
		fields["reports"] = []map[string]any{{"name": "r", "label": "R", "template": "t", "data_handler": "h", "formats": []string{"pdf"}, "permissions": []string{}}}
	case "automation":
		fields["subscribes"] = []map[string]any{{"name": "demo.order.created"}}
	case "field_extension":
		fields["view_extensions"] = []map[string]any{{"extends": "core", "extension": "ext"}}
		fields["schema"] = map[string]any{
			"owned_models":   []string{},
			"extends_module": "core",
			"extends_models": []string{"sales.order"},
		}
	}

	return fields
}

func TestLoadManifestModuleTypeConstraintsValid(t *testing.T) {
	for _, moduleType := range []string{"domain", "l10n", "connector", "bridge", "theme", "report_bundle", "automation", "field_extension"} {
		t.Run(moduleType, func(t *testing.T) {
			m, err := json.Marshal(validTypeFields(moduleType))
			if err != nil {
				t.Fatalf("marshal fixture: %v", err)
			}
			if _, err := Load(m); err != nil {
				t.Fatalf("expected a valid %s manifest to load, got %v", moduleType, err)
			}
		})
	}
}

func TestLoadManifestModuleTypeConstraintsViolations(t *testing.T) {
	cases := map[string]struct {
		moduleType  string
		mutate      func(map[string]any)
		wantErrText string
	}{
		"domain wasm required": {
			"domain",
			func(f map[string]any) { f["wasm"] = false },
			`requires wasm: true`,
		},
		"l10n owns_schema forbidden": {
			"l10n",
			func(f map[string]any) { f["schema"] = map[string]any{"owned_models": []string{"l10n.chart"}} },
			`must not declare schema.owned_models`,
		},
		"connector wasm required": {
			"connector",
			func(f map[string]any) { f["wasm"] = false },
			`requires wasm: true`,
		},
		"bridge wasm required": {
			"bridge",
			func(f map[string]any) { f["wasm"] = false },
			`requires wasm: true`,
		},
		"bridge owns_schema forbidden": {
			"bridge",
			func(f map[string]any) { f["schema"] = map[string]any{"owned_models": []string{"bridge.link"}} },
			`must not declare schema.owned_models`,
		},
		"bridge has_ui forbidden": {
			"bridge",
			func(f map[string]any) {
				f["views"] = []map[string]any{{"name": "x", "type": "list", "resource": "y", "label": "z"}}
			},
			`must not declare views or a frontend bundle`,
		},
		"bridge requires depends_on >= 2": {
			"bridge",
			func(f map[string]any) { f["depends_on"] = []string{"mod_a"} },
			`requires depends_on length >= 2`,
		},
		"theme requires wasm false": {
			"theme",
			func(f map[string]any) { f["wasm"] = true },
			`requires wasm: false`,
		},
		"theme owns_schema forbidden": {
			"theme",
			func(f map[string]any) { f["schema"] = map[string]any{"owned_models": []string{"theme.tokens"}} },
			`must not declare schema.owned_models`,
		},
		"theme has_ui required": {
			"theme",
			func(f map[string]any) { delete(f, "views") },
			`requires a view or a frontend bundle`,
		},
		"report_bundle owns_schema forbidden": {
			"report_bundle",
			func(f map[string]any) { f["schema"] = map[string]any{"owned_models": []string{"reports.thing"}} },
			`must not declare schema.owned_models`,
		},
		"report_bundle has_ui forbidden": {
			"report_bundle",
			func(f map[string]any) {
				f["views"] = []map[string]any{{"name": "x", "type": "list", "resource": "y", "label": "z"}}
			},
			`must not declare views or a frontend bundle`,
		},
		"report_bundle requires reports >= 1": {
			"report_bundle",
			func(f map[string]any) { delete(f, "reports") },
			`requires reports length >= 1`,
		},
		"automation wasm required": {
			"automation",
			func(f map[string]any) { f["wasm"] = false },
			`requires wasm: true`,
		},
		"automation owns_schema forbidden": {
			"automation",
			func(f map[string]any) { f["schema"] = map[string]any{"owned_models": []string{"automation.thing"}} },
			`must not declare schema.owned_models`,
		},
		"automation has_ui forbidden": {
			"automation",
			func(f map[string]any) {
				f["views"] = []map[string]any{{"name": "x", "type": "list", "resource": "y", "label": "z"}}
			},
			`must not declare views or a frontend bundle`,
		},
		"automation requires subscribes >= 1": {
			"automation",
			func(f map[string]any) { delete(f, "subscribes") },
			`requires subscribes length >= 1`,
		},
		"field_extension owns_schema forbidden": {
			"field_extension",
			func(f map[string]any) {
				f["schema"] = map[string]any{
					"owned_models":   []string{"ext.thing"},
					"extends_module": "core",
					"extends_models": []string{"sales.order"},
				}
			},
			`must not declare schema.owned_models`,
		},
		"field_extension requires view_extensions or view_extension_definitions": {
			"field_extension",
			func(f map[string]any) { delete(f, "view_extensions") },
			`requires view_extensions or view_extension_definitions`,
		},
		"field_extension requires extends_module": {
			"field_extension",
			func(f map[string]any) {
				f["schema"] = map[string]any{"owned_models": []string{}, "extends_models": []string{"sales.order"}}
			},
			`requires schema.extends_module`,
		},
		"field_extension requires extends_models": {
			"field_extension",
			func(f map[string]any) {
				f["schema"] = map[string]any{"owned_models": []string{}, "extends_module": "core"}
			},
			`requires schema.extends_models`,
		},
	}

	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			fields := validTypeFields(c.moduleType)
			c.mutate(fields)

			m, err := json.Marshal(fields)
			if err != nil {
				t.Fatalf("marshal fixture: %v", err)
			}

			_, err = Load(m)
			if err == nil {
				t.Fatalf("expected a manifest violating %q to be rejected", name)
			}
			if !strings.Contains(err.Error(), c.wantErrText) {
				t.Fatalf("expected error to contain %q, got %v", c.wantErrText, err)
			}
		})
	}
}

func TestLoadManifestInvalidUtf8(t *testing.T) {
	m := []byte(`{"name": "demo`)
	m = append(m, 0xff, 0xfe) // invalid UTF-8 sequence

	_, err := Load(m)
	if !errors.Is(err, ErrInvalidUtf8) {
		t.Fatalf("expected ErrInvalidUtf8, got %v", err)
	}
}

func TestLoadManifestRejectsComments(t *testing.T) {
	cases := map[string][]byte{
		"line comment":  []byte("{\n  // this is a comment\n  \"name\": \"demo\"\n}"),
		"block comment": []byte("{\n  /* comment */\n  \"name\": \"demo\"\n}"),
	}

	for name, m := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Load(m)
			if !errors.Is(err, ErrInvalidJSON) {
				t.Fatalf("expected ErrInvalidJSON for %s, got %v", name, err)
			}
		})
	}
}

func TestLoadManifestOversized(t *testing.T) {
	// valid JSON string padded past the 1MB limit
	padding := bytes.Repeat([]byte("a"), MB)
	m := append([]byte(`{"name": "`), padding...)
	m = append(m, []byte(`"}`)...)

	_, err := Load(m)
	if !errors.Is(err, ErrManifestTooLarge) {
		t.Fatalf("expected ErrManifestTooLarge, got %v", err)
	}
}

func TestLoadManifestAtSizeLimit(t *testing.T) {
	// exactly 1MB of valid JSON should still be rejected — len(m) >= MB
	padding := bytes.Repeat([]byte("a"), MB-len(`{"name":""}`))
	m := append([]byte(`{"name":"`), padding...)
	m = append(m, []byte(`"}`)...)

	if len(m) != MB {
		t.Fatalf("test setup error: manifest is %d bytes, want exactly %d", len(m), MB)
	}

	_, err := Load(m)
	if !errors.Is(err, ErrManifestTooLarge) {
		t.Fatalf("expected a manifest at exactly the size limit to be rejected, got %v", err)
	}
}

func TestManifestJSONDecodesCoreFields(t *testing.T) {
	payload := []byte(`{
		"name": "demo",
		"display_name": "Demo",
		"type": "domain",
		"version": "1.0.0",
		"description": "A demo module",
		"abi_version": "1",
		"engine": ">=1.0.0 <2.0.0",
		"depends_on": [],
		"capabilities": ["db.read"],
		"schema": {
			"owned_models": ["sales.order"],
			"extends_module": "core",
			"extends_models": ["sales.order"],
			"has_data_migrations": true
		},
		"frontend": {
			"bundle": true,
			"entry": "frontend/src/index.ts",
			"bundle_sha256": "sha256:deadbeef"
		},
		"emits": [{"name": "demo.order.created", "version": 1, "description": "created"}],
		"subscribes": [{"name": "demo.order.created", "version": 1, "async": true, "idempotency_key_field": "event_id", "retry_policy": {"max_attempts": 5, "backoff": "exponential", "initial_delay_ms": 1000}}],
		"permissions": [{"name": "demo:orders:read", "description": "Read orders", "category": "sales", "default_roles": ["user"]}],
		"policies": [{"name": "tenant_policy", "description": "Tenant policy", "applies_to": "sales.order", "condition": "record.tenant_id = current_user.tenant_id"}],
		"views": [{"name": "contacts_list", "type": "list", "resource": "contacts.contact", "label": "Contacts", "columns": [{"field": "display_name", "label": "Name", "type": "text"}], "filters": [{"field": "state", "label": "Status", "type": "select", "options": [{"value": "draft", "label": "Draft"}]}], "actions": [{"label": "New Contact", "type": "create", "view": "contacts_form"}]}],
		"view_extension_definitions": [{"name": "hr_employment_tab", "type": "tab", "target_section": "tabs", "position": "append", "tab": {"label": "Employment", "icon": "briefcase", "type": "component", "permission": "hr:employee:read", "condition": "record.user_id != null", "component": "EmploymentTab"}}],
		"analytics_projections": [{"name": "orders_daily", "type": "clickhouse_table", "source_model": "sales.order", "sync": "event_driven", "events": ["sale.order.updated"]}],
		"oauth_provider": {"name": "github", "label": "GitHub", "scopes": ["read:user"]},
		"webhook_adapters": ["demo.order.created"],
		"checksum": "sha256:empty"
	}`)

	var got Manifest
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatalf("unmarshal manifest: %v", err)
	}

	if got.Name != "demo" {
		t.Fatalf("expected name to decode, got %q", got.Name)
	}
	if got.Schema.OwnedModels[0] != "sales.order" {
		t.Fatalf("expected owned model to decode, got %v", got.Schema.OwnedModels)
	}
	if got.Schema.ExtendsModule == nil || *got.Schema.ExtendsModule != "core" {
		t.Fatalf("expected extends module to decode, got %v", got.Schema.ExtendsModule)
	}
	if !got.Schema.HasDataMigrations {
		t.Fatalf("expected has_data_migrations to decode")
	}
	if !*got.Frontend.Bundle {
		t.Fatalf("expected frontend bundle flag to decode")
	}
	if got.Frontend.Entry != "frontend/src/index.ts" {
		t.Fatalf("expected frontend entry to decode, got %q", got.Frontend.Entry)
	}
	if got.Frontend.BundleSHA256 != "sha256:deadbeef" {
		t.Fatalf("expected frontend hash to decode, got %q", got.Frontend.BundleSHA256)
	}
	if len(got.Emits) != 1 || got.Emits[0].Name != "demo.order.created" {
		t.Fatalf("expected emitted event to decode, got %#v", got.Emits)
	}
	if len(got.Subscribes) != 1 || !got.Subscribes[0].Async {
		t.Fatalf("expected subscription to decode, got %#v", got.Subscribes)
	}
	if len(got.Permissions) != 1 || got.Permissions[0].Name != "demo:orders:read" {
		t.Fatalf("expected permission to decode, got %#v", got.Permissions)
	}
	if len(got.Policies) != 1 || got.Policies[0].AppliesTo != "sales.order" {
		t.Fatalf("expected policy to decode, got %#v", got.Policies)
	}
	if len(got.Views) != 1 || got.Views[0].Type != "list" {
		t.Fatalf("expected list view to decode, got %#v", got.Views)
	}
	if len(got.Views[0].Columns) != 1 || got.Views[0].Columns[0].Field != "display_name" {
		t.Fatalf("expected list column to decode, got %#v", got.Views[0].Columns)
	}
	if len(got.Views[0].Filters) != 1 || got.Views[0].Filters[0].Field != "state" {
		t.Fatalf("expected filter to decode, got %#v", got.Views[0].Filters)
	}
	if len(got.Views[0].Actions) != 1 || got.Views[0].Actions[0].View != "contacts_form" {
		t.Fatalf("expected action to decode, got %#v", got.Views[0].Actions)
	}
	if len(got.ViewExtensionDefinitions) != 1 {
		t.Fatalf("expected view extension definition to decode, got %#v", got.ViewExtensionDefinitions)
	}
	if got.ViewExtensionDefinitions[0].Tab == nil || got.ViewExtensionDefinitions[0].Tab.Label != "Employment" {
		t.Fatalf("expected tab payload to decode, got %#v", got.ViewExtensionDefinitions[0].Tab)
	}
	if len(got.AnalyticsProjections) != 1 {
		t.Fatalf("expected analytics projection to decode, got %#v", got.AnalyticsProjections)
	}
	if got.OAuthProvider == nil || got.OAuthProvider.Name != "github" {
		t.Fatalf("expected oauth provider to decode, got %#v", got.OAuthProvider)
	}
	if len(got.WebhookAdapters) != 1 || got.WebhookAdapters[0] != "demo.order.created" {
		t.Fatalf("expected webhook adapter to decode, got %#v", got.WebhookAdapters)
	}
}

func TestAuditedTableUnmarshalJSON(t *testing.T) {
	payload := []byte(`["contacts", {"table": "invoices", "exclude_columns": ["etag", "updated_at"]}]`)

	var got []AuditedTable
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatalf("unmarshal audited tables: %v", err)
	}

	if len(got) != 2 {
		t.Fatalf("expected 2 audited tables, got %d", len(got))
	}
	if got[0].Table != "contacts" || got[0].ExcludeColumns != nil {
		t.Fatalf("expected bare string form to decode with no exclusions, got %#v", got[0])
	}
	if got[1].Table != "invoices" || len(got[1].ExcludeColumns) != 2 || got[1].ExcludeColumns[0] != "etag" {
		t.Fatalf("expected object form to decode with exclusions, got %#v", got[1])
	}
}

func TestAuditedTableMarshalJSON(t *testing.T) {
	tables := []AuditedTable{
		{Table: "contacts"},
		{Table: "invoices", ExcludeColumns: []string{"etag", "updated_at"}},
	}

	out, err := json.Marshal(tables)
	if err != nil {
		t.Fatalf("marshal audited tables: %v", err)
	}

	want := `[` +
		`"contacts",` +
		`{"table":"invoices","exclude_columns":["etag","updated_at"]}` +
		`]`
	if string(out) != want {
		t.Fatalf("expected %s, got %s", want, out)
	}
}

func TestLoadManifestValidatesRetryPolicy(t *testing.T) {
	baseRetryPolicy := func() map[string]any {
		return map[string]any{
			"max_attempts":     5,
			"backoff":          "exponential",
			"initial_delay_ms": 1000,
		}
	}

	cases := map[string]struct {
		mutate func(map[string]any)
	}{
		"max_attempts too low": {
			mutate: func(rp map[string]any) { rp["max_attempts"] = 0 },
		},
		"max_attempts too high": {
			mutate: func(rp map[string]any) { rp["max_attempts"] = 26 },
		},
		"backoff not in enum": {
			mutate: func(rp map[string]any) { rp["backoff"] = "fixed" },
		},
		"initial_delay_ms below minimum": {
			mutate: func(rp map[string]any) { rp["initial_delay_ms"] = 50 },
		},
	}

	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			rp := baseRetryPolicy()
			c.mutate(rp)

			fields := minimalManifestFields()
			fields["subscribes"] = []map[string]any{
				{"name": "demo.order.created", "async": true, "retry_policy": rp},
			}

			m, err := json.Marshal(fields)
			if err != nil {
				t.Fatalf("marshal fixture: %v", err)
			}
			if _, err := Load(m); err == nil {
				t.Fatalf("expected invalid retry_policy (%s) to be rejected", name)
			}
		})
	}
}

func TestLoadManifestAcceptsValidRetryPolicy(t *testing.T) {
	fields := minimalManifestFields()
	fields["subscribes"] = []map[string]any{
		{
			"name":    "demo.order.created",
			"async":   true,
			"handler": "handle_order_created",
			"retry_policy": map[string]any{
				"max_attempts":     5,
				"backoff":          "exponential",
				"initial_delay_ms": 1000,
			},
		},
	}

	m, err := json.Marshal(fields)
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	if _, err := Load(m); err != nil {
		t.Fatalf("expected valid retry_policy to be accepted, got %v", err)
	}
}
