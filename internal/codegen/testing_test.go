package codegen

import (
	"bytes"
	"encoding/json/v2"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTestingFixture_PreservesSchemaAndFunctionMappings(t *testing.T) {
	in, err := InputFromSchema([]byte(`{
		"engine_version":"dev", "schema_hash":"hash", "modules": {
			"contacts": {"permissions":[], "models": {
				"contacts.contact":{"label_plural":"Contacts", "enabled_ops":["list","get","update","delete"], "fields":[]}
			}, "views":[{"name":"contacts_list","type":"list","resource":"contacts.contact","columns":[{"field":"name"}]}],
			"routes":[
				{"method":"GET","path":"/contacts/people","model":"contacts.contact","name":"list","crud_action":"list","response_is_list":true},
				{"method":"GET","path":"/contacts/people/{id}","model":"contacts.contact","crud_action":"get","engine_native":true},
				{"method":"PATCH","path":"/contacts/people/{id}","model":"contacts.contact","name":"update","crud_action":"update"},
				{"method":"DELETE","path":"/contacts/people/{id}","model":"contacts.contact","crud_action":"delete","engine_native":true},
				{"method":"GET","path":"/contacts/by-email/{email}","response_is_list":true}
			]},
			"foreign":{"models":{},"routes":[],"views":[]}
		}}
	`), "contacts")
	if err != nil {
		t.Fatal(err)
	}

	_, fixture, err := GenerateArtifacts(in)
	if err != nil {
		t.Fatal(err)
	}
	var parsed struct {
		Schema    map[string]any             `json:"schema"`
		Functions map[string]TestingFunction `json:"functions"`
	}
	if err := json.Unmarshal(fixture, &parsed); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(fixture), `"columns"`) || !strings.Contains(string(fixture), `"foreign"`) {
		t.Fatalf("fixture loses complete view or foreign schema: %s", fixture)
	}
	list := parsed.Functions["listContacts"]
	if list.Path != "/contacts/people" || list.Response != "paged" || list.Parameters[0].Source != "list_query" {
		t.Fatalf("list mapping = %+v", list)
	}
	update := parsed.Functions["updateContact"]
	if update.Method != "PATCH" || len(update.Parameters) != 3 || update.Parameters[2].Source != "etag" {
		t.Fatalf("update mapping = %+v", update)
	}
	raw := parsed.Functions["getByEmail"]
	if raw.Path != "/contacts/by-email/{email}" || raw.Parameters[0].WireName != "email" || raw.Parameters[1].Source != "query" {
		t.Fatalf("raw mapping = %+v", raw)
	}

	for range 10 {
		_, again, err := GenerateArtifacts(in)
		if err != nil || !bytes.Equal(again, fixture) {
			t.Fatalf("fixture is not deterministic: %v", err)
		}
	}
}

func TestTestingFixture_LocalIncludesSynthesizedViews(t *testing.T) {
	dir := copyExampleModule(t)
	path := filepath.Join(dir, "cmd", "module", "main.go")
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	updated := strings.Replace(string(source), "EnableOps(model.List, model.Get, model.Create, model.Update, model.Delete),", "EnableOps(model.List, model.Get, model.Create, model.Update, model.Delete).EnableViews(model.ListView, model.FormView),", 1)
	if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(dir, "manifest.json")
	manifestJSON, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var manifest map[string]any
	if err := json.Unmarshal(manifestJSON, &manifest); err != nil {
		t.Fatal(err)
	}
	delete(manifest, "views")
	manifestJSON, err = json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, manifestJSON, 0o644); err != nil {
		t.Fatal(err)
	}
	in, err := LoadLocal(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	_, fixture, err := GenerateArtifacts(in)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"contacts_contact_list"`, `"contacts_contact_form"`, `"columns"`, `"sections"`, `"/contacts/contacts/{id}"`} {
		if !strings.Contains(string(fixture), want) {
			t.Errorf("fixture lacks %s", want)
		}
	}
}
