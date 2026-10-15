package engine

import (
	"encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/route"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

func TestDispatchORMRoute_DynamicLinkResponses(t *testing.T) {
	f := newDispatchORMFixture(t)
	md := widgetModelDecl()
	md.Fields = append(md.Fields,
		model.NamedField{Name: "reference_type", Def: model.Selection("contacts.contact")},
		model.NamedField{Name: "reference_id", Def: model.DynamicLink("reference_type")},
	)
	contact := model.Define("contacts.contact", model.Table("contact")).
		Field("id", model.UUID().PrimaryKey()).
		Field("display_name", model.Text()).
		Field("deleted_at", model.TimestampTZ())
	if _, err := f.e.moduleRegistry.Update(map[string]*module.LoadedModule{
		"testmodule": {
			Status:       module.StatusReady,
			Manifest:     manifest.Manifest{Name: "testmodule", Type: "standard"},
			ModelDecls:   []model.ModelDeclaration{md},
			Capabilities: abi.CapDBRead | abi.CapDBWrite,
		},
		"contacts": {
			Status:     module.StatusReady,
			Manifest:   manifest.Manifest{Name: "contacts", Type: "standard"},
			ModelDecls: []model.ModelDeclaration{*contact},
		},
	}); err != nil {
		t.Fatal(err)
	}

	const targetID = "00000000-0000-0000-0000-000000000101"
	schema := "tenant_" + f.slug
	for _, stmt := range []string{
		`ALTER TABLE ` + schema + `.widget ADD COLUMN reference_type TEXT, ADD COLUMN reference_id UUID`,
		`CREATE TABLE ` + schema + `.contact (id UUID PRIMARY KEY, display_name TEXT, deleted_at TIMESTAMPTZ)`,
		`INSERT INTO ` + schema + `.contact VALUES ('` + targetID + `', 'Contact', NULL)`,
	} {
		if _, err := f.e.primaryDB.ExecContext(t.Context(), stmt); err != nil {
			t.Fatal(err)
		}
	}

	request := func(method, path, id string, body []byte, entry *route.RouteEntry, status int) map[string]any {
		t.Helper()
		w := httptest.NewRecorder()
		f.e.dispatchORMRoute(w, f.request(method, path, body, entry, map[string]string{"id": id}))
		if w.Code != status {
			t.Fatalf("%s %s: status %d, body %s", method, path, w.Code, w.Body.String())
		}

		var record map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &record); err != nil {
			t.Fatal(err)
		}
		return record
	}
	assertReference := func(record map[string]any) {
		t.Helper()
		reference, ok := record["reference"].(map[string]any)
		if record["reference_id"] != targetID || !ok || reference["id"] != targetID || reference["display_name"] != "Contact" {
			t.Fatalf("DynamicLink response: %+v", record)
		}
	}

	created := request(http.MethodPost, "/testmodule/widgets", "", []byte(`{"name":"Comment","reference_type":"contacts.contact","reference_id":"`+targetID+`"}`), f.entryCreate, http.StatusCreated)
	assertReference(created)

	id, _ := created["id"].(string)
	path := "/testmodule/widgets/" + id
	assertReference(request(http.MethodGet, path, id, nil, f.entryGet, http.StatusOK))
	assertReference(request(http.MethodPut, path, id, []byte(`{"name":"Updated comment"}`), f.entryUpdate, http.StatusOK))

	listed := request(http.MethodGet, "/testmodule/widgets?fields[widget]=reference_id", "", nil, f.entryList, http.StatusOK)
	data, ok := listed["data"].([]any)
	if !ok || len(data) != 1 {
		t.Fatalf("list response: %+v", listed)
	}
	record, ok := data[0].(map[string]any)
	if !ok || len(record) != 2 {
		t.Fatalf("projected list response: %+v", data[0])
	}
	assertReference(record)

	if _, err := f.e.primaryDB.ExecContext(t.Context(), "UPDATE "+schema+".contact SET deleted_at = NOW() WHERE id = $1", targetID); err != nil {
		t.Fatal(err)
	}
	deleted := request(http.MethodGet, path, id, nil, f.entryGet, http.StatusOK)
	if value, present := deleted["reference"]; !present || value != nil || deleted["reference_id"] != targetID {
		t.Fatalf("deleted target response: %+v", deleted)
	}
}
