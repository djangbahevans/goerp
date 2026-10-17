package contacts_test

import (
	"fmt"
	"maps"
	"net/http"
	"slices"
	"sync"
	"testing"
	"uuid"

	"github.com/djangbahevans/goerp/modules/contacts/schema"
	"github.com/djangbahevans/goerp/sdk/go/modeltest"
)

const identifierTypesPath = "/contacts/identifier-types"
const identifiersPath = "/contacts/identifiers"

func createIdentifierType(t *testing.T, h *modeltest.Harness, code string, extra map[string]any) string {
	t.Helper()
	body := map[string]any{"code": code, "label": code}
	maps.Copy(body, extra)
	response := h.POST(identifierTypesPath, body)
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("create identifier type: %d %v", response.StatusCode, response.JSON("error"))
	}
	return response.JSON("id").(string)
}

func createIdentifier(t *testing.T, h *modeltest.Harness, contactID, typeID, value string) string {
	t.Helper()
	response := h.POST(identifiersPath, map[string]any{"contact_id": contactID, "type_id": typeID, "value": value})
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("create identifier: %d %v", response.StatusCode, response.JSON("error"))
	}
	return response.JSON("id").(string)
}

func requireIdentifierStatus(t *testing.T, response *modeltest.Response, status int) {
	t.Helper()
	if response.StatusCode != status {
		t.Fatalf("status=%d error=%v, want %d", response.StatusCode, response.JSON("error"), status)
	}
}

func TestIdentifierNormalizationPatternAndIndexes(t *testing.T) {
	h := modeltest.NewHarness(t)
	person := createContact(t, h, map[string]any{"type": "person", "given_name": "Ama"})
	other := createContact(t, h, map[string]any{"type": "person", "given_name": "Esi"})
	typ := createIdentifierType(t, h, "passport", map[string]any{"uppercase": true, "pattern": "[A-Z]{2}[0-9]{3}", "applies_to": "person"})
	id := createIdentifier(t, h, person, typ, "  ab123\t")
	response := h.GET(identifiersPath + "/" + id)
	if response.JSON("value") != "AB123" || response.JSON("unique_key") != "AB123" || response.JSON("is_tax") != false || response.JSON("is_sensitive") != false {
		t.Fatalf("identifier=%v", response.JSON("value"))
	}

	for _, value := range []string{"prefixAB123", "AB123suffix", "", "A123"} {
		requireIdentifierStatus(t, h.POST(identifiersPath, map[string]any{"contact_id": other, "type_id": typ, "value": value}), 400)
	}
	requireIdentifierStatus(t, h.POST(identifiersPath, map[string]any{"contact_id": other, "type_id": typ, "value": "ab123"}), 409)
	requireIdentifierStatus(t, h.POST(identifiersPath, map[string]any{"contact_id": person, "type_id": typ, "value": "AB124"}), 409)
	org := createContact(t, h, map[string]any{"type": "organization", "legal_name": "Acme"})
	requireIdentifierStatus(t, h.POST(identifiersPath, map[string]any{"contact_id": org, "type_id": typ, "value": "AB125"}), 400)
	for _, name := range []string{"idx_contact_identifiers_unique", "idx_contact_identifiers_one_per_type", "idx_contact_identifiers_one_tax", "idx_contact_identifier_types_code"} {
		h.DB.AssertIndexExists(name)
	}
	for _, field := range []string{"unique_key", "is_tax", "is_sensitive"} {
		requireIdentifierStatus(t, h.PUT(identifiersPath+"/"+id, map[string]any{field: nil}), 422)
	}
	requireIdentifierStatus(t, h.DELETE(identifiersPath+"/"+id), 204)
	createIdentifier(t, h, other, typ, "AB123")
}

func TestIdentifierTaxIDAndMoves(t *testing.T) {
	h := modeltest.NewHarness(t)
	first := createContact(t, h, map[string]any{"type": "person", "given_name": "Ama"})
	second := createContact(t, h, map[string]any{"type": "person", "given_name": "Esi"})
	tax := createIdentifierType(t, h, "tax", map[string]any{"is_tax": true})
	otherTax := createIdentifierType(t, h, "other-tax", map[string]any{"is_tax": true})
	id := createIdentifier(t, h, first, tax, "TX-1")
	if got := h.GET(contactsPath + "/" + first).JSON("tax_id"); got != "TX-1" {
		t.Fatalf("tax_id=%v", got)
	}
	requireIdentifierStatus(t, h.POST(identifiersPath, map[string]any{"contact_id": first, "type_id": otherTax, "value": "OTHER"}), 409)
	requireIdentifierStatus(t, h.PUT(identifiersPath+"/"+id, map[string]any{"contact_id": second, "value": "TX-2"}), 200)
	if got := h.GET(contactsPath + "/" + first).JSON("tax_id"); got != nil {
		t.Errorf("source tax_id=%v", got)
	}
	if got := h.GET(contactsPath + "/" + second).JSON("tax_id"); got != "TX-2" {
		t.Errorf("target tax_id=%v", got)
	}
	requireIdentifierStatus(t, h.DELETE(identifiersPath+"/"+id), 204)
	if got := h.GET(contactsPath + "/" + second).JSON("tax_id"); got != nil {
		t.Errorf("archived tax_id=%v", got)
	}
}

func TestIdentifierTypeRuleChangesRewriteAndRollback(t *testing.T) {
	h := modeltest.NewHarness(t)
	first := createContact(t, h, map[string]any{"type": "person", "given_name": "Ama"})
	second := createContact(t, h, map[string]any{"type": "person", "given_name": "Esi"})
	typ := createIdentifierType(t, h, "reference", map[string]any{"is_unique": false})
	one := createIdentifier(t, h, first, typ, "ab123")
	two := createIdentifier(t, h, second, typ, "ab123")
	requireIdentifierStatus(t, h.PUT(identifierTypesPath+"/"+typ, map[string]any{"is_unique": true}), 409)
	if h.GET(identifierTypesPath+"/"+typ).JSON("is_unique") != false || h.GET(identifiersPath+"/"+one).JSON("unique_key") != nil {
		t.Fatal("failed uniqueness change leaked")
	}
	requireIdentifierStatus(t, h.PUT(identifiersPath+"/"+two, map[string]any{"value": "cd456"}), 200)
	requireIdentifierStatus(t, h.PUT(identifierTypesPath+"/"+typ, map[string]any{"uppercase": true, "is_unique": true, "pattern": "[A-Z]{2}[0-9]{3}", "is_tax": true}), 200)
	for id, want := range map[string]string{one: "AB123", two: "CD456"} {
		row := h.GET(identifiersPath + "/" + id)
		if row.JSON("value") != want || row.JSON("unique_key") != want || row.JSON("is_tax") != true {
			t.Errorf("rewrite: value=%v key=%v tax=%v", row.JSON("value"), row.JSON("unique_key"), row.JSON("is_tax"))
		}
	}
	if got := h.GET(contactsPath + "/" + first).JSON("tax_id"); got != "AB123" {
		t.Errorf("tax_id after rule change=%v", got)
	}
	for _, patch := range []map[string]any{{"pattern": "[0-9]+"}, {"pattern": "["}, {"applies_to": "organization"}, {"is_sensitive": true}} {
		response := h.PUT(identifierTypesPath+"/"+typ, patch)
		if response.StatusCode != 400 {
			t.Fatalf("rule patch %v: status=%d error=%v", patch, response.StatusCode, response.JSON("error"))
		}
	}
	if row := h.GET(identifierTypesPath + "/" + typ); row.JSON("pattern") != "[A-Z]{2}[0-9]{3}" || row.JSON("applies_to") != "any" || row.JSON("is_sensitive") != false {
		t.Fatal("failed rule change leaked")
	}
	requireIdentifierStatus(t, h.PUT(identifierTypesPath+"/"+typ, map[string]any{"is_tax": false, "is_unique": false}), 200)
	if row := h.GET(identifiersPath + "/" + one); row.JSON("unique_key") != nil || row.JSON("is_tax") != false {
		t.Fatal("flags were not cleared")
	}
	if got := h.GET(contactsPath + "/" + first).JSON("tax_id"); got != nil {
		t.Errorf("tax_id after clearing tax=%v", got)
	}
}

func TestIdentifierSensitiveWritesAndOwnedTypes(t *testing.T) {
	h := modeltest.NewHarness(t)
	contact := createContact(t, h, map[string]any{"type": "person", "given_name": "Ama"})
	typ := createIdentifierType(t, h, "national-id", map[string]any{"is_sensitive": true})
	ordinary := h.WithPermissions(schema.ContactRead, schema.IdentifierRead, schema.IdentifierWrite)
	requireIdentifierStatus(t, ordinary.POST(identifiersPath, map[string]any{"contact_id": contact, "type_id": typ, "value": "123"}), 403)
	id := createIdentifier(t, h, contact, typ, "123")
	requireIdentifierStatus(t, ordinary.PUT(identifiersPath+"/"+id, map[string]any{"value": "456"}), 403)
	requireIdentifierStatus(t, ordinary.DELETE(identifiersPath+"/"+id), 403)
	requireIdentifierStatus(t, h.WithPermissions(schema.ContactRead, schema.ConfigWrite, schema.IdentifierRead).PUT(identifierTypesPath+"/"+typ, map[string]any{"pattern": "[0-9]+"}), 403)
	requireIdentifierStatus(t, h.WithPermissions(schema.ContactRead).POST(identifierTypesPath, map[string]any{"code": "denied", "label": "Denied"}), 403)

	h.DB.Seed("contact_identifier_types", map[string]any{"code": "owned", "label": "Owned", "owner_module": "l10n_gh", "pattern": "[A-Z]+"})
	var owned struct{ ID string }
	h.DB.QueryOne(&owned, `SELECT id FROM contact_identifier_types WHERE code='owned'`)
	for _, patch := range []map[string]any{{"code": "other"}, {"pattern": ".*"}, {"uppercase": true}, {"is_unique": false}, {"is_tax": true}, {"is_sensitive": true}, {"applies_to": "person"}, {"country_code": "GH"}} {
		requireIdentifierStatus(t, h.PUT(identifierTypesPath+"/"+owned.ID, patch), 400)
	}
	requireIdentifierStatus(t, h.DELETE(identifierTypesPath+"/"+owned.ID), 400)
	requireIdentifierStatus(t, h.PUT(identifierTypesPath+"/"+owned.ID, map[string]any{"label": "New label", "is_active": false}), 200)
	requireIdentifierStatus(t, h.POST(identifierTypesPath, map[string]any{"code": "fake-owned", "label": "Fake", "owner_module": "l10n_gh"}), 400)
}

func TestIdentifierInactiveTypesAndEtag(t *testing.T) {
	h := modeltest.NewHarness(t)
	contact := createContact(t, h, map[string]any{"type": "person", "given_name": "Ama"})
	typ := createIdentifierType(t, h, "reference", nil)
	id := createIdentifier(t, h, contact, typ, "ABC")
	requireIdentifierStatus(t, h.PUT(identifierTypesPath+"/"+typ, map[string]any{"is_active": false}), 200)
	requireIdentifierStatus(t, h.POST(identifiersPath, map[string]any{"contact_id": contact, "type_id": typ, "value": "NEW"}), 400)
	requireIdentifierStatus(t, h.PUT(identifiersPath+"/"+id, map[string]any{"value": "UPDATED", "expires_on": "2027-01-01"}), 200)
	requireIdentifierStatus(t, h.WithHeader("If-Match", "stale").PUT(identifiersPath+"/"+id, map[string]any{"value": "STALE"}), 409)
	if row := h.GET(identifiersPath + "/" + id); row.JSON("value") != "UPDATED" || row.JSON("expires_on") != "2027-01-01" {
		t.Errorf("identifier after stale write: value=%v expires=%v", row.JSON("value"), row.JSON("expires_on"))
	}
}

func TestIdentifierTypeSensitivityAndArchivedType(t *testing.T) {
	h := modeltest.NewHarness(t)
	contact := createContact(t, h, map[string]any{"type": "person", "given_name": "Ama"})
	typ := createIdentifierType(t, h, "reference", nil)
	id := createIdentifier(t, h, contact, typ, "ABC")
	for _, sensitive := range []bool{true, false} {
		requireIdentifierStatus(t, h.PUT(identifierTypesPath+"/"+typ, map[string]any{"is_sensitive": sensitive}), 200)
		if got := h.GET(identifiersPath + "/" + id).JSON("is_sensitive"); got != sensitive {
			t.Fatalf("is_sensitive=%v, want %v", got, sensitive)
		}
	}
	requireIdentifierStatus(t, h.DELETE(identifierTypesPath+"/"+typ), 204)
	requireIdentifierStatus(t, h.DELETE(identifiersPath+"/"+id), 204)
}

func TestIdentifierRuleChangesVisitAllPages(t *testing.T) {
	h := modeltest.NewHarness(t)
	typ := createIdentifierType(t, h, "reference", map[string]any{"is_unique": false})
	var contacts, identifiers []map[string]any
	for i := range 105 {
		contactID := uuid.New().String()
		contacts = append(contacts, map[string]any{"id": contactID, "type": "person", "given_name": "Person"})
		identifiers = append(identifiers, map[string]any{"contact_id": contactID, "type_id": typ, "value": fmt.Sprintf("ref-%d", i)})
	}
	h.DB.Seed("contacts", contacts)
	h.DB.Seed("contact_identifiers", identifiers)

	requireIdentifierStatus(t, h.PUT(identifierTypesPath+"/"+typ, map[string]any{"uppercase": true}), 200)
	h.DB.AssertCount("contact_identifiers", 105, "value = upper(value)")
}

func TestConcurrentUniqueIdentifiers(t *testing.T) {
	h := modeltest.NewHarness(t)
	typ := createIdentifierType(t, h, "unique", map[string]any{"uppercase": true})
	var contacts [2]string
	for i := range 2 {
		contacts[i] = createContact(t, h, map[string]any{"type": "person", "given_name": fmt.Sprintf("Person %d", i)})
	}
	var statuses [2]int
	var group sync.WaitGroup
	for i := range 2 {
		group.Go(func() {
			statuses[i] = h.POST(identifiersPath, map[string]any{"contact_id": contacts[i], "type_id": typ, "value": "same"}).StatusCode
		})
	}
	group.Wait()
	slices.Sort(statuses[:])
	if statuses != [2]int{201, 409} {
		t.Fatalf("concurrent unique creates: %v", statuses)
	}
	h.DB.AssertCount("contact_identifiers", 1, "deleted_at IS NULL")
}
