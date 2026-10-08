package codegen

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// metaSchemaFixture is a /_meta/schema response: contacts declares
// contacts.contact, crm extends it with an industry field (model.Extend)
// and declares an action a contacts view references.
const metaSchemaFixture = `{
  "engine_version": "dev",
  "schema_hash": "h",
  "modules": {
    "contacts": {
      "name": "contacts",
      "load_order": 0,
      "routes": [
        {"method": "GET", "path": "/contacts/contacts", "model": "contacts.contact", "crud_action": "list", "response_is_list": true, "permissions": [], "engine_native": true},
        {"method": "GET", "path": "/contacts/contacts/{id}", "model": "contacts.contact", "crud_action": "get", "response_is_list": false, "permissions": [], "engine_native": true},
        {"method": "POST", "path": "/contacts/contacts/{id}/archive", "model": "contacts.contact", "name": "archive", "response_is_list": false, "permissions": []},
        {"method": "POST", "path": "/contacts/contacts/merge", "model": "contacts.contact", "name": "merge", "response_is_list": false, "permissions": [],
         "request_type": {"kind": "object", "name": "MergeContactsRequest", "fields": [
           {"name": "target_id", "type": {"kind": "string"}},
           {"name": "source_ids", "type": {"kind": "array", "elem": {"kind": "string"}}}
         ]},
         "response_type": {"kind": "object", "name": "Contact", "fields": [{"name": "id", "type": {"kind": "string"}}]}},
        {"method": "GET", "path": "/contacts/by-email/{email}", "response_is_list": false, "permissions": []},
        {"method": "GET", "path": "/contacts/search", "model": "contacts.contact", "crud_action": "list", "response_is_list": true, "permissions": []},
        {"method": "POST", "path": "/contacts/contacts/{id}/approve", "model": "contacts.contact", "name": "approve", "crud_action": "workflow_transition", "response_is_list": false, "permissions": [], "engine_native": true},
        {"method": "GET", "path": "/contacts/live", "response_is_list": false, "permissions": [], "websocket": true}
      ],
      "views": [
        {"name": "contacts_list", "type": "list", "resource": "contacts.contact", "label": "Contacts",
         "bulk_actions": [{"label": "Mark won", "type": "route", "route": "crm.markWon"}]}
      ],
      "models": {
        "contacts.contact": {
          "name": "contact", "label": "Contact", "label_plural": "Contacts", "enabled_ops": ["list", "get"], "shareable": false,
          "fields": [
            {"name": "id", "type": "uuid", "primary_key": true, "readonly": true, "has_default": true},
            {"name": "name", "type": "text", "required": true},
            {"name": "kind", "type": "selection", "selection_values": ["person", "company"], "required": true}
          ]
        }
      }
    },
    "crm": {
      "name": "crm",
      "load_order": 1,
      "routes": [
        {"method": "POST", "path": "/crm/leads/{id}/markWon", "model": "crm.lead", "name": "markWon", "response_is_list": false, "permissions": []}
      ],
      "views": [],
      "models": {
        "crm.lead": {"name": "lead", "label_plural": "Leads", "enabled_ops": [], "fields": []},
        "contacts.contact": {"name": "contact", "enabled_ops": [], "fields": [
          {"name": "name", "type": "text"},
          {"name": "industry", "type": "char"}
        ]}
      }
    }
  }
}`

func TestInputFromSchema_GeneratesWithExtendFields(t *testing.T) {
	in, err := InputFromSchema([]byte(metaSchemaFixture), "contacts")
	if err != nil {
		t.Fatalf("InputFromSchema() error: %v", err)
	}
	out := generateString(t, in)

	for _, want := range []string{
		// crm's model.Extend field, after the model's own fields; crm's
		// own "name" doesn't replace the base declaration.
		"  name: string;\n  kind: 'person' | 'company';\n  industry: string | null;\n};",
		"listContacts: (params?: ListParams<Contact>)",
		"getContact: (id: string): Promise<Contact>",
		"archiveContact: (id: string, body?: unknown): Promise<unknown>",
		"mergeContact: (body: MergeContactsRequest): Promise<Contact>",
		"export interface MergeContactsRequest",
		"getByEmail: (email: string, params?: Record<string, unknown>)",
		"`/contacts/by-email/${encodeURIComponent(email)}`",
		// A raw route bound to a model op the model also enables is still
		// a raw route; only the engine-native EnableOps route isn't.
		"getSearch: (params?: Record<string, unknown>): Promise<PagedResponse<unknown>>",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	for _, absent := range []string{"getLive", "Lead", "interface Contact", "approveContact", "getContacts"} {
		if strings.Contains(out, absent) {
			t.Errorf("output contains %q:\n%s", absent, out)
		}
	}
}

func TestInputFromSchema_ValidatesAgainstOtherModules(t *testing.T) {
	broken := strings.Replace(metaSchemaFixture, `"route": "crm.markWon"`, `"route": "crm.markLost"`, 1)
	in, err := InputFromSchema([]byte(broken), "contacts")
	if err != nil {
		t.Fatalf("InputFromSchema() error: %v", err)
	}
	_, err = Generate(in)
	if err == nil || !strings.Contains(err.Error(), `module crm declares no action named "markLost"`) {
		t.Fatalf("Generate() error = %v, want an unknown crm action", err)
	}
}

func TestInputFromSchema_RawModelBindingDoesNotProvideCRUD(t *testing.T) {
	raw := `{"modules":{"contacts":{
		"models":{"contacts.contact":{"label_plural":"Contacts","enabled_ops":[],"fields":[]}},
		"routes":[{"method":"GET","path":"/contacts/search","model":"contacts.contact","crud_action":"list"}],
		"views":[{"name":"contacts_list","type":"list","resource":"contacts.contact"}]
	}}}`
	in, err := InputFromSchema([]byte(raw), "contacts")
	if err != nil {
		t.Fatalf("InputFromSchema() error: %v", err)
	}

	_, err = Generate(in)
	if err == nil || !strings.Contains(err.Error(), `resource contacts.contact has no "list" op`) {
		t.Fatalf("Generate() error = %v, want a missing list op", err)
	}
}

func TestInputFromSchema_UnknownModule(t *testing.T) {
	if _, err := InputFromSchema([]byte(metaSchemaFixture), "billing"); err == nil || !strings.Contains(err.Error(), "billing is not loaded") {
		t.Fatalf("InputFromSchema(billing) error = %v, want a not-loaded error", err)
	}
}

func TestFetchSchema_SendsAPIKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/_meta/schema" || r.Header.Get("Authorization") != "Bearer erp_key" {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(metaSchemaFixture))
	}))
	defer srv.Close()

	raw, err := FetchSchema(t.Context(), srv.Client(), srv.URL+"/", "erp_key")
	if err != nil || string(raw) != metaSchemaFixture {
		t.Fatalf("FetchSchema() = %d bytes, %v; want the schema", len(raw), err)
	}
	if _, err := FetchSchema(t.Context(), srv.Client(), srv.URL, "wrong"); err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("FetchSchema(wrong key) error = %v, want a 401", err)
	}
}
