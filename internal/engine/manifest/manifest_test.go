package manifest

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"
)

func TestLoadManifestValidMinimal(t *testing.T) {
	m := []byte(`{"name": "demo", "version": "1.0.0"}`)

	if _, err := LoadManifest(m); err != nil {
		t.Fatalf("expected valid minimal manifest to pass, got %v", err)
	}
}

func TestLoadManifestInvalidUtf8(t *testing.T) {
	m := []byte(`{"name": "demo`)
	m = append(m, 0xff, 0xfe) // invalid UTF-8 sequence

	_, err := LoadManifest(m)
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
			_, err := LoadManifest(m)
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

	_, err := LoadManifest(m)
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

	_, err := LoadManifest(m)
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
	if !got.Frontend.Bundle {
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
