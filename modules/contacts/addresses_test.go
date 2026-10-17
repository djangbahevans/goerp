package contacts_test

import (
	"maps"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
	"uuid"

	"github.com/djangbahevans/goerp/modules/contacts/schema"
	"github.com/djangbahevans/goerp/sdk/go/modeltest"
)

const addressesPath = "/contacts/addresses"

func createAddress(t *testing.T, h *modeltest.Harness, contactID, city, country string, extra map[string]any) string {
	t.Helper()

	body := map[string]any{"contact_id": contactID, "city": city, "country_code": country}
	maps.Copy(body, extra)
	response := h.POST(addressesPath, body)
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("create address: status=%d error=%v", response.StatusCode, response.JSON("error"))
	}
	return response.JSON("id").(string)
}

func assertPrimaryAddress(t *testing.T, h *modeltest.Harness, contactID, addressID string, city, country any) {
	t.Helper()

	contact := h.GET(contactsPath + "/" + contactID)
	if contact.StatusCode != 200 || contact.JSON("city") != city || contact.JSON("country_code") != country {
		t.Fatalf("contact: status=%d city=%v country=%v; want %v, %v; error=%v", contact.StatusCode, contact.JSON("city"), contact.JSON("country_code"), city, country, contact.JSON("error"))
	}

	var primary struct {
		ID    string
		Count int
	}
	h.DB.QueryOne(&primary, `SELECT coalesce(min(id::text), '') AS id, count(*) AS count FROM contact_addresses WHERE contact_id=$1 AND is_primary AND deleted_at IS NULL`, contactID)
	wantCount := 0
	if addressID != "" {
		wantCount = 1
	}
	if primary.ID != addressID || primary.Count != wantCount {
		t.Errorf("primary=%s count=%d, want %s count=%d", primary.ID, primary.Count, addressID, wantCount)
	}
}

func writeAddress(t *testing.T, h *modeltest.Harness, id string, body map[string]any) {
	t.Helper()

	response := h.PUT(addressesPath+"/"+id, body)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("update address: status=%d error=%v", response.StatusCode, response.JSON("error"))
	}
}

func TestAddressPrimariesAndStoredContactValues(t *testing.T) {
	h := modeltest.NewHarness(t)
	contact := createContact(t, h, map[string]any{"type": "person", "given_name": "Ama"})
	assertPrimaryAddress(t, h, contact, "", nil, nil)
	first := createAddress(t, h, contact, "Accra", "GH", nil)
	second := createAddress(t, h, contact, "Lagos", "NG", map[string]any{"type": "billing", "label": "Office", "street": "1 High Street", "street2": "Floor 2", "postal_code": "100001"})
	assertPrimaryAddress(t, h, contact, first, "Accra", "GH")
	if response := h.GET(addressesPath + "/" + first); response.JSON("type") != "other" || response.JSON("is_primary") != true {
		t.Errorf("address defaults: type=%v primary=%v", response.JSON("type"), response.JSON("is_primary"))
	}
	response := h.GET(addressesPath + "/" + second)
	for key, want := range map[string]any{"type": "billing", "label": "Office", "street": "1 High Street", "street2": "Floor 2", "postal_code": "100001", "is_primary": false} {
		if response.JSON(key) != want {
			t.Errorf("address %s=%v, want %v", key, response.JSON(key), want)
		}
	}

	writeAddress(t, h, second, map[string]any{"is_primary": true})
	assertPrimaryAddress(t, h, contact, second, "Lagos", "NG")
	writeAddress(t, h, second, map[string]any{"city": "Abuja", "country_code": "GH"})
	assertPrimaryAddress(t, h, contact, second, "Abuja", "GH")
	event := h.Events.Last(schema.ContactUpdated).Payload
	if !slices.Contains(event.ChangedFields, "city") || !slices.Contains(event.ChangedFields, "country_code") {
		t.Errorf("contact update event=%+v", event)
	}
	writeAddress(t, h, second, map[string]any{"city": nil, "country_code": nil})
	assertPrimaryAddress(t, h, contact, second, nil, nil)

	for _, id := range []string{second, first} {
		if response := h.DELETE(addressesPath + "/" + id); response.StatusCode != http.StatusNoContent {
			t.Fatalf("archive address: status=%d error=%v", response.StatusCode, response.JSON("error"))
		}
		if id == second {
			assertPrimaryAddress(t, h, contact, first, "Accra", "GH")
		} else {
			assertPrimaryAddress(t, h, contact, "", nil, nil)
		}
	}
	h.DB.AssertIndexExists("idx_contact_addresses_primary")
}

func TestAddressMovesDemotionAndPromotionOrder(t *testing.T) {
	h := modeltest.NewHarness(t)
	contact := createContact(t, h, map[string]any{"type": "person", "given_name": "Ama"})
	other := createContact(t, h, map[string]any{"type": "person", "given_name": "Esi"})
	first := createAddress(t, h, contact, "Accra", "GH", nil)
	second := createAddress(t, h, contact, "Kumasi", "GH", nil)
	var secondRow struct{ CreatedAt time.Time }
	h.DB.QueryOne(&secondRow, `SELECT created_at FROM contact_addresses WHERE id=$1`, second)
	third := uuid.New().String()
	h.DB.Seed("contact_addresses", map[string]any{
		"id": third, "contact_id": contact, "city": "Tema", "country_code": "GH", "created_at": secondRow.CreatedAt,
	})
	writeAddress(t, h, first, map[string]any{"is_primary": false})
	assertPrimaryAddress(t, h, contact, first, "Accra", "GH")
	writeAddress(t, h, first, map[string]any{"contact_id": other})
	wantID, wantCity := second, "Kumasi"
	if third < second {
		wantID, wantCity = third, "Tema"
	}
	assertPrimaryAddress(t, h, contact, wantID, wantCity, "GH")
	assertPrimaryAddress(t, h, other, first, "Accra", "GH")
	writeAddress(t, h, first, map[string]any{"contact_id": contact})
	assertPrimaryAddress(t, h, other, "", nil, nil)
	assertPrimaryAddress(t, h, contact, first, "Accra", "GH")

	if response := h.DELETE(addressesPath + "/" + first); response.StatusCode != 204 {
		t.Fatalf("archive primary: %d", response.StatusCode)
	}
	assertPrimaryAddress(t, h, contact, wantID, wantCity, "GH")
}

func TestAddressValidationAndEtagRollback(t *testing.T) {
	h := modeltest.NewHarness(t)
	contact := createContact(t, h, map[string]any{"type": "person", "given_name": "Ama"})
	first := createAddress(t, h, contact, "Accra", "GH", nil)
	second := createAddress(t, h, contact, "Kumasi", "GH", nil)
	for _, body := range []map[string]any{
		{"type": "office", "is_primary": true},
		{"contact_id": nil, "is_primary": true},
		{"contact_id": "invalid", "is_primary": true},
		{"is_primary": "yes"},
		{"unknown": "value"},
		{"created_at": "2026-01-01T00:00:00Z"},
	} {
		response := h.PUT(addressesPath+"/"+second, body)
		if response.StatusCode != 400 {
			t.Errorf("invalid %v: status=%d error=%v", body, response.StatusCode, response.JSON("error"))
		}
		assertPrimaryAddress(t, h, contact, first, "Accra", "GH")
	}
	response := h.WithHeader("If-Match", "stale").PUT(addressesPath+"/"+second, map[string]any{"is_primary": true})
	if response.StatusCode != 409 {
		t.Errorf("stale etag: status=%d error=%v", response.StatusCode, response.JSON("error"))
	}
	assertPrimaryAddress(t, h, contact, first, "Accra", "GH")
}

func TestConcurrentFirstAddressesHaveOnePrimary(t *testing.T) {
	h := modeltest.NewHarness(t)
	contact := createContact(t, h, map[string]any{"type": "organization", "legal_name": "Acme"})
	var responses [2]*modeltest.Response
	var wg sync.WaitGroup
	for i := range 2 {
		wg.Go(func() {
			parentID := contact
			if i == 1 {
				parentID = strings.ToUpper(parentID)
			}
			responses[i] = h.POST(addressesPath, map[string]any{"contact_id": parentID, "city": "Accra", "country_code": "GH"})
		})
	}
	wg.Wait()
	for _, response := range responses {
		if response.StatusCode != 201 {
			t.Fatalf("concurrent create: status=%d error=%v", response.StatusCode, response.JSON("error"))
		}
	}
	h.DB.AssertCount("contact_addresses", 1, "is_primary AND deleted_at IS NULL")
	h.DB.AssertCount("contact_addresses", 2, "deleted_at IS NULL")
}

func TestAddressPermissionsAndParentVisibility(t *testing.T) {
	h := modeltest.NewHarness(t)
	contact := createContact(t, h, map[string]any{"type": "person", "given_name": "Ama"})
	address := createAddress(t, h, contact, "Accra", "GH", nil)
	reader := h.WithPermissions(schema.ContactRead)
	if response := reader.GET(addressesPath + "/" + address); response.StatusCode != 200 {
		t.Fatalf("read address: status=%d", response.StatusCode)
	}
	for _, response := range []*modeltest.Response{
		reader.POST(addressesPath, map[string]any{"contact_id": contact}),
		reader.PUT(addressesPath+"/"+address, map[string]any{"city": "Kumasi"}),
		reader.DELETE(addressesPath + "/" + address),
	} {
		if response.StatusCode != 403 {
			t.Errorf("read-only address mutation: status=%d", response.StatusCode)
		}
	}
	assertPrimaryAddress(t, h, contact, address, "Accra", "GH")
	if response := h.WithPermissions(schema.ContactRead, schema.ContactWrite).DELETE(addressesPath + "/" + address); response.StatusCode != 204 {
		t.Fatalf("archive with contact write: status=%d error=%v", response.StatusCode, response.JSON("error"))
	}

	other := modeltest.NewHarness(t)
	foreign := createContact(t, other, map[string]any{"type": "person", "given_name": "Other tenant"})
	if response := h.POST(addressesPath, map[string]any{"contact_id": foreign}); response.StatusCode != 404 {
		t.Errorf("foreign parent: status=%d error=%v", response.StatusCode, response.JSON("error"))
	}
	if response := h.DELETE(contactsPath + "/" + contact); response.StatusCode != 204 {
		t.Fatalf("archive contact: status=%d", response.StatusCode)
	}
	if response := h.POST(addressesPath, map[string]any{"contact_id": contact}); response.StatusCode != 404 {
		t.Errorf("archived parent: status=%d error=%v", response.StatusCode, response.JSON("error"))
	}
}
