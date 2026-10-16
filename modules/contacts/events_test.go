package contacts_test

import (
	"maps"
	"net/http"
	"slices"
	"testing"

	"github.com/djangbahevans/goerp/modules/contacts/schema"
	"github.com/djangbahevans/goerp/sdk/go/events"
	"github.com/djangbahevans/goerp/sdk/go/modeltest"
)

var contactCreatedWire = events.Define[map[string]any](schema.ContactCreated.Name())

func TestCreateEmitsContactCreated(t *testing.T) {
	h := modeltest.NewHarness(t)
	id := createContact(t, h, map[string]any{
		"type": "person", "given_name": "Ama", "family_name": "Mensah", "preferred_name": "Mimi",
		"date_of_birth": "2000-01-01", "gender": "woman",
	})

	h.Events.AssertEmittedN(schema.ContactCreated, 1)
	evt := h.Events.Last(schema.ContactCreated)
	if evt.Version != 1 || evt.Payload.ContactID != id || evt.Payload.Type != "person" || evt.Payload.DisplayName != "Mimi Mensah" || evt.Payload.Phone != nil || evt.Payload.Email != nil {
		t.Errorf("event = %+v", evt)
	}
	assertEventFields(t, h.Events.Last(contactCreatedWire).Payload, "contact_id", "type", "display_name", "phone", "email")
}

func TestUpdateEmitsContactUpdatedWithChangedFields(t *testing.T) {
	h := modeltest.NewHarness(t)
	id := createContact(t, h, map[string]any{"type": "organization", "legal_name": "Acme Ltd"})
	resp := h.PUT(contactsPath+"/"+id, map[string]any{"legal_name": "Acme Holdings"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("update: status = %d, error = %v", resp.StatusCode, resp.JSON("error"))
	}

	h.Events.AssertEmittedN(schema.ContactUpdated, 1)
	payload := h.Events.Last(schema.ContactUpdated).Payload
	if payload.ContactID != id || payload.DisplayName != "Acme Holdings" || payload.Email != nil || payload.Phone != nil {
		t.Errorf("payload = %+v", payload)
	}
	if !slices.Contains(payload.ChangedFields, "legal_name") {
		t.Errorf("changed_fields = %v, want legal_name", payload.ChangedFields)
	}
	assertEventFields(t, h.Events.Last(events.Define[map[string]any](schema.ContactUpdated.Name())).Payload, "contact_id", "changed_fields", "display_name", "phone", "email")
}

func TestDeleteEmitsContactDeleted(t *testing.T) {
	h := modeltest.NewHarness(t)
	id := createContact(t, h, map[string]any{"type": "organization", "legal_name": "Acme Ltd"})
	if resp := h.DELETE(contactsPath + "/" + id); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete: status = %d", resp.StatusCode)
	}

	h.Events.AssertEmittedN(schema.ContactDeleted, 1)
	payload := h.Events.Last(schema.ContactDeleted).Payload
	if payload.ContactID != id || payload.DisplayName != "Acme Ltd" {
		t.Errorf("payload = %+v", payload)
	}
	assertEventFields(t, h.Events.Last(events.Define[map[string]any](schema.ContactDeleted.Name())).Payload, "contact_id", "display_name")
}

func TestRejectedWriteEmitsNoEvent(t *testing.T) {
	h := modeltest.NewHarness(t)
	response := h.POST(contactsPath, map[string]any{"type": "person", "preferred_name": "Mimi"})
	requireRejected(t, response, "a person needs a given name or family name")
	h.Events.AssertEmittedN(schema.ContactCreated, 0)
}

func assertEventFields(t *testing.T, payload map[string]any, fields ...string) {
	t.Helper()
	slices.Sort(fields)
	if got := slices.Sorted(maps.Keys(payload)); !slices.Equal(got, fields) {
		t.Errorf("event fields = %v, want %v", got, fields)
	}
}
