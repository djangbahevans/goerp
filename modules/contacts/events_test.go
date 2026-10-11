package contacts_test

import (
	"net/http"
	"reflect"
	"slices"
	"testing"

	"github.com/djangbahevans/goerp/modules/contacts/schema"
	"github.com/djangbahevans/goerp/sdk/go/events"
	"github.com/djangbahevans/goerp/sdk/go/modeltest"
)

var contactCreatedWire = events.Define[map[string]any](schema.ContactCreated.Name())

func TestCreateEmitsContactCreated(t *testing.T) {
	h := modeltest.NewHarness(t)
	company := createContact(t, h, map[string]any{"name": "Acme Ltd"})
	h.Events.AssertEmittedN(schema.ContactCreated, 1)

	id := createContact(t, h, map[string]any{
		"type":       "person",
		"name":       "Ama Mensah",
		"email":      "ama@example.test",
		"phone":      "0200000000",
		"company_id": company,
	})

	h.Events.AssertEmittedN(schema.ContactCreated, 2)
	evt := h.Events.Last(schema.ContactCreated)
	if evt.Version != 1 {
		t.Errorf("version = %d, want 1", evt.Version)
	}

	want := schema.ContactCreatedPayload{
		ContactID: id,
		Type:      "person",
		Name:      "Ama Mensah",
		Email:     new("ama@example.test"),
		Phone:     new("0200000000"),
		CompanyID: new(company),
	}
	if !reflect.DeepEqual(evt.Payload, want) {
		t.Errorf("payload = %+v, want %+v", evt.Payload, want)
	}
	if _, present := h.Events.Last(contactCreatedWire).Payload["id"]; present {
		t.Error("payload carries the record's id key; the wire key is contact_id")
	}
}

func TestUpdateEmitsContactUpdatedWithChangedFields(t *testing.T) {
	h := modeltest.NewHarness(t)
	id := createContact(t, h, map[string]any{"name": "Acme Ltd", "email": "info@acme.test"})
	etag, _ := h.GET(contactsPath + "/" + id).JSON("etag").(string)

	resp := h.PUT(contactsPath+"/"+id, map[string]any{"name": "Acme Holdings", "etag": etag})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("update: status = %d, error = %v %v", resp.StatusCode, resp.JSON("error.code"), resp.JSON("error.message"))
	}

	h.Events.AssertEmittedN(schema.ContactUpdated, 1)
	payload := h.Events.Last(schema.ContactUpdated).Payload
	if payload.ContactID != id || payload.Name != "Acme Holdings" || (payload.Email == nil || *payload.Email != "info@acme.test") {
		t.Errorf("payload = %v, want contact_id %s, the new name and the current email", payload, id)
	}

	if !slices.Equal(payload.ChangedFields, []string{"name"}) {
		t.Errorf("changed_fields = %v, want only [name]", payload.ChangedFields)
	}
}

func TestDeleteEmitsContactDeleted(t *testing.T) {
	h := modeltest.NewHarness(t)
	id := createContact(t, h, map[string]any{"name": "Acme Ltd"})

	if resp := h.DELETE(contactsPath + "/" + id); resp.StatusCode >= 400 {
		t.Fatalf("delete: status = %d", resp.StatusCode)
	}

	h.Events.AssertEmittedN(schema.ContactDeleted, 1)
	payload := h.Events.Last(schema.ContactDeleted).Payload
	if payload.ContactID != id || payload.Name != "Acme Ltd" {
		t.Errorf("payload = %v, want contact_id %s and the archived contact's name", payload, id)
	}
}

func TestRejectedWriteEmitsNoEvent(t *testing.T) {
	h := modeltest.NewHarness(t)

	company := createContact(t, h, map[string]any{"name": "Acme Ltd"})
	response := h.POST(contactsPath, map[string]any{
		"type":       "company",
		"name":       "Sub Ltd",
		"company_id": company,
	})
	requireRejected(t, response, onlyPersonMessage)

	h.Events.AssertEmittedN(schema.ContactCreated, 1)
}
