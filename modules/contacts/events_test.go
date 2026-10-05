package contacts_test

import (
	"net/http"
	"slices"
	"testing"

	"github.com/djangbahevans/goerp/sdk/go/modeltest"
)

func TestCreateEmitsContactCreated(t *testing.T) {
	h := modeltest.NewHarness(t)
	company := createContact(t, h, map[string]any{"name": "Acme Ltd"})
	h.Events.AssertEmittedN("contacts.contact.created", 1)

	id := createContact(t, h, map[string]any{
		"type": "person", "name": "Ama Mensah", "email": "ama@example.test", "phone": "0200000000", "company_id": company,
	})

	h.Events.AssertEmittedN("contacts.contact.created", 2)
	evt := h.Events.Last("contacts.contact.created")
	if evt.Version != 1 {
		t.Errorf("version = %d, want 1", evt.Version)
	}
	for key, want := range map[string]any{
		"contact_id": id, "type": "person", "name": "Ama Mensah",
		"email": "ama@example.test", "phone": "0200000000", "company_id": company,
	} {
		if got := evt.Payload[key]; got != want {
			t.Errorf("payload %s = %v, want %v", key, got, want)
		}
	}
	if _, present := evt.Payload["id"]; present {
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

	h.Events.AssertEmittedN("contacts.contact.updated", 1)
	payload := h.Events.Last("contacts.contact.updated").Payload
	if payload["contact_id"] != id || payload["name"] != "Acme Holdings" || payload["email"] != "info@acme.test" {
		t.Errorf("payload = %v, want contact_id %s, the new name and the current email", payload, id)
	}
	changed, _ := payload["changed_fields"].([]any)
	if !slices.Equal(changed, []any{"name"}) {
		t.Errorf("changed_fields = %v, want only [name]", changed)
	}
}

func TestDeleteEmitsContactDeleted(t *testing.T) {
	h := modeltest.NewHarness(t)
	id := createContact(t, h, map[string]any{"name": "Acme Ltd"})

	if resp := h.DELETE(contactsPath + "/" + id); resp.StatusCode >= 400 {
		t.Fatalf("delete: status = %d", resp.StatusCode)
	}

	h.Events.AssertEmittedN("contacts.contact.deleted", 1)
	payload := h.Events.Last("contacts.contact.deleted").Payload
	if payload["contact_id"] != id || payload["name"] != "Acme Ltd" {
		t.Errorf("payload = %v, want contact_id %s and the archived contact's name", payload, id)
	}
}

func TestRejectedWriteEmitsNoEvent(t *testing.T) {
	h := modeltest.NewHarness(t)

	requireRejected(t, h.POST(contactsPath, map[string]any{"type": "company", "name": "Sub Ltd", "company_id": createContact(t, h, map[string]any{"name": "Acme Ltd"})}), onlyPersonMessage)

	h.Events.AssertEmittedN("contacts.contact.created", 1)
}
