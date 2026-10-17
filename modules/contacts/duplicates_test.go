package contacts_test

import (
	"fmt"
	"net/http"
	"reflect"
	"slices"
	"testing"

	"github.com/djangbahevans/goerp/modules/contacts/schema"
	"github.com/djangbahevans/goerp/sdk/go/modeltest"
	"github.com/djangbahevans/goerp/sdk/go/perm"
)

const duplicatesPath = contactsPath + "/duplicates"

func duplicateMatches(t *testing.T, response *modeltest.Response) []map[string]any {
	t.Helper()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("duplicates: status=%d error=%v", response.StatusCode, response.JSON("error"))
	}
	var matches []map[string]any
	response.ParseJSON(&matches)
	return matches
}

func requireDuplicateReasons(t *testing.T, matches []map[string]any, id string, reasons ...string) {
	t.Helper()
	if len(matches) != 1 || matches[0]["id"] != id {
		t.Fatalf("matches=%v, want contact %s", matches, id)
	}
	want := make([]any, len(reasons))
	for i, reason := range reasons {
		want[i] = reason
	}
	if !reflect.DeepEqual(matches[0]["reasons"], want) {
		t.Errorf("reasons=%v, want %v", matches[0]["reasons"], want)
	}
	if len(matches[0]) != 6 {
		t.Errorf("match fields=%v, want id, display_name, type, phone, email, reasons", matches[0])
	}
}

func TestDuplicatesEachRuleAndAllReasons(t *testing.T) {
	h := modeltest.NewHarness(t)
	setTenantCountry(t, h, "GH")
	id := createContact(t, h, map[string]any{
		"type": "person", "given_name": "Ama", "other_names": "Akua", "family_name": "Mensah", "preferred_name": "Mimi",
	})
	createChannel(t, h, id, "email", "primary@example.test", nil)
	createChannel(t, h, id, "email", "shared@example.test", nil)
	createChannel(t, h, id, "phone", "020 000 0000", nil)
	createChannel(t, h, id, "phone", "024 412 3456", nil)
	typ := createIdentifierType(t, h, "reference", map[string]any{"uppercase": true, "is_unique": false})
	createIdentifier(t, h, id, typ, "REF-123")
	identifiers := []map[string]any{{"type_id": typ, "value": " ref-123\t"}}

	for _, tc := range []struct {
		body   map[string]any
		reason string
	}{
		{map[string]any{"type": "person", "identifiers": identifiers}, "identifier"},
		{map[string]any{"type": "organization", "email": "SHARED@example.test"}, "email"},
		{map[string]any{"type": "organization", "phone": "+233244123456"}, "phone"},
		{map[string]any{"type": "person", "given_name": " Ama ", "other_names": "Akua", "family_name": "Mensah"}, "name"},
	} {
		t.Run(tc.reason, func(t *testing.T) {
			matches := duplicateMatches(t, h.POST(duplicatesPath, tc.body))
			requireDuplicateReasons(t, matches, id, tc.reason)
			if matches[0]["display_name"] != "Mimi Mensah" || matches[0]["phone"] != "020 000 0000" || matches[0]["email"] != "primary@example.test" {
				t.Errorf("match=%v, want display name and primary channels", matches[0])
			}
		})
	}

	body := map[string]any{
		"type": "person", "given_name": "Ama", "other_names": "Akua", "family_name": "Mensah",
		"email": "shared@example.test", "phone": "+233244123456", "identifiers": identifiers,
	}
	requireDuplicateReasons(t, duplicateMatches(t, h.POST(duplicatesPath, body)), id, "identifier", "email", "phone", "name")
	h.DB.AssertCount("contacts", 1, "")
	h.DB.AssertCount("contact_channels", 4, "")
	createContact(t, h, map[string]any{"type": "person", "given_name": "Ama", "other_names": "Akua", "family_name": "Mensah"})
	h.DB.AssertCount("contacts", 2, "")
}

func TestDuplicatesNameSimilarityAndType(t *testing.T) {
	h := modeltest.NewHarness(t)
	id := createContact(t, h, map[string]any{"type": "organization", "legal_name": "abcdef", "trading_name": "Shop"})
	createContact(t, h, map[string]any{"type": "person", "given_name": "abcde"})
	for _, tc := range []struct {
		name string
		want bool
	}{
		{"ABCDE", true},
		{"abcd", false},
		{"abcdef", true},
		{"Shop", false},
		{"'; DROP TABLE contacts; --", false},
	} {
		matches := duplicateMatches(t, h.POST(duplicatesPath, map[string]any{"type": "organization", "legal_name": tc.name}))
		if tc.want {
			requireDuplicateReasons(t, matches, id, "name")
		} else if len(matches) != 0 {
			t.Errorf("name=%q matches=%v, want none", tc.name, matches)
		}
	}

	boundary := createContact(t, h, map[string]any{"type": "organization", "legal_name": "abcde xyz"})
	requireDuplicateReasons(t, duplicateMatches(t, h.POST(duplicatesPath, map[string]any{
		"type": "organization", "legal_name": "abcde", "exclude_id": id,
	})), boundary, "name")
}

func TestDuplicatesIdentifiersRequireTheSameTypeAndNormalization(t *testing.T) {
	h := modeltest.NewHarness(t)
	id := createContact(t, h, map[string]any{"type": "person", "given_name": "Ama"})
	typ := createIdentifierType(t, h, "case-sensitive", map[string]any{"uppercase": false})
	otherType := createIdentifierType(t, h, "other", nil)
	identifier := createIdentifier(t, h, id, typ, "Ab123")
	for _, tc := range []struct {
		typeID, value string
		want          bool
	}{
		{typ, "  Ab123  ", true},
		{typ, "ab123", false},
		{otherType, "Ab123", false},
		{typ, "", false},
		{"00000000-0000-0000-0000-000000000001", "Ab123", false},
	} {
		matches := duplicateMatches(t, h.POST(duplicatesPath, map[string]any{
			"type": "person", "identifiers": []map[string]any{{"type_id": tc.typeID, "value": tc.value}},
		}))
		if tc.want {
			requireDuplicateReasons(t, matches, id, "identifier")
		} else if len(matches) != 0 {
			t.Errorf("type=%s value=%q matches=%v, want none", tc.typeID, tc.value, matches)
		}
	}

	if response := h.DELETE(identifiersPath + "/" + identifier); response.StatusCode != http.StatusNoContent {
		t.Fatal(response.JSON("error"))
	}
	matches := duplicateMatches(t, h.POST(duplicatesPath, map[string]any{
		"type": "person", "identifiers": []map[string]any{{"type_id": typ, "value": "Ab123"}},
	}))
	if len(matches) != 0 {
		t.Errorf("archived identifier matches=%v, want none", matches)
	}
}

func TestDuplicatesIdentifierPermissions(t *testing.T) {
	h := modeltest.NewHarness(t)
	id := createContact(t, h, map[string]any{"type": "person", "given_name": "Ama"})
	typ := createIdentifierType(t, h, "private", map[string]any{"is_sensitive": true})
	createIdentifier(t, h, id, typ, "SECRET")
	body := map[string]any{"type": "person", "identifiers": []map[string]any{{"type_id": typ, "value": "SECRET"}}}

	for _, tc := range []struct {
		name        string
		permissions []perm.Permission
		want        bool
	}{
		{"contact reader", []perm.Permission{schema.ContactRead}, false},
		{"identifier reader", []perm.Permission{schema.ContactRead, schema.IdentifierRead}, false},
		{"personal reader alone", []perm.Permission{schema.ContactRead, schema.PersonalRead}, false},
		{"personal writer", []perm.Permission{schema.ContactRead, schema.IdentifierRead, schema.IdentifierWrite, schema.PersonalWrite}, false},
		{"personal reader", []perm.Permission{schema.ContactRead, schema.IdentifierRead, schema.PersonalRead}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			matches := duplicateMatches(t, h.WithPermissions(tc.permissions...).POST(duplicatesPath, body))
			if tc.want {
				requireDuplicateReasons(t, matches, id, "identifier")
			} else if len(matches) != 0 {
				t.Errorf("matches=%v, want no sensitive identifier matches", matches)
			}
		})
	}
	response := h.WithPermissions().POST(duplicatesPath, body)
	if response.StatusCode != http.StatusForbidden || response.ErrorCode() != "permission_denied" {
		t.Errorf("unauthorized status=%d error=%s", response.StatusCode, response.ErrorCode())
	}
}

func TestDuplicatesPhoneCountryAndArchivedChannels(t *testing.T) {
	h := modeltest.NewHarness(t)
	id := createContact(t, h, map[string]any{"type": "person", "given_name": "Ama"})
	createChannel(t, h, id, "phone", "+233 (24) 412-3456", nil)
	for _, country := range []string{"", "GH", "US"} {
		setTenantCountry(t, h, country)
		matches := duplicateMatches(t, h.POST(duplicatesPath, map[string]any{"type": "person", "phone": "0244123456"}))
		if country == "GH" {
			requireDuplicateReasons(t, matches, id, "phone")
		} else if len(matches) != 0 {
			t.Errorf("country=%q matches=%v, want none", country, matches)
		}
		requireDuplicateReasons(t, duplicateMatches(t, h.POST(duplicatesPath, map[string]any{"type": "person", "phone": "233244123456"})), id, "phone")
	}
	channel := createChannel(t, h, id, "email", "archived@example.test", nil)
	if response := h.DELETE(channelsPath + "/" + channel); response.StatusCode != http.StatusNoContent {
		t.Fatal(response.JSON("error"))
	}
	for _, body := range []map[string]any{
		{"type": "person", "email": "archived@example.test"},
		{"type": "person", "phone": "+()"},
		{"type": "person", "phone": "phone 233244123456"},
		{"type": "person"},
	} {
		if matches := duplicateMatches(t, h.POST(duplicatesPath, body)); len(matches) != 0 {
			t.Errorf("body=%v matches=%v, want none", body, matches)
		}
	}
}

func TestDuplicatesExcludesInactiveArchivedAndSelfAndCapsResults(t *testing.T) {
	h := modeltest.NewHarness(t)
	var ids []string
	for range 14 {
		ids = append(ids, createContact(t, h, map[string]any{"type": "organization", "legal_name": "Acme"}))
	}
	slices.Sort(ids)
	if response := h.PUT(contactsPath+"/"+ids[0], map[string]any{"is_active": false}); response.StatusCode != http.StatusOK {
		t.Fatal(response.JSON("error"))
	}
	if response := h.DELETE(contactsPath + "/" + ids[1]); response.StatusCode != http.StatusNoContent {
		t.Fatal(response.JSON("error"))
	}
	matches := duplicateMatches(t, h.POST(duplicatesPath, map[string]any{"type": "organization", "legal_name": "Acme", "exclude_id": ids[2]}))
	if len(matches) != 10 {
		t.Fatalf("matches=%d, want 10", len(matches))
	}
	for i, match := range matches {
		if match["id"] != ids[i+3] {
			t.Errorf("match[%d]=%v, want %s", i, match["id"], ids[i+3])
		}
	}
}

func TestDuplicatesContinuesPastTheFirstContactAndChildPages(t *testing.T) {
	h := modeltest.NewHarness(t)
	for i := range 205 {
		id := fmt.Sprintf("00000000-0000-0000-0000-%012d", i+1)
		h.DB.Seed("contacts", map[string]any{
			"id": id, "type": "person", "given_name": "Ama", "full_name": "Ama", "display_name": "Ama", "sort_name": "Ama",
		})
	}
	id := "00000000-0000-0000-0000-000000000205"
	for i := range 205 {
		value := fmt.Sprintf("unrelated%d@example.test", i)
		if i == 204 {
			value = "match@example.test"
		}
		h.DB.Seed("contact_channels", map[string]any{
			"id": fmt.Sprintf("00000000-0000-0000-0000-%012d", i+1), "contact_id": id, "kind": "email", "value": value,
		})
	}
	requireDuplicateReasons(t, duplicateMatches(t, h.POST(duplicatesPath, map[string]any{
		"type": "person", "email": "match@example.test",
	})), id, "email")

	for i := range 205 {
		typeID := fmt.Sprintf("00000000-0000-0000-0000-%012d", i+1)
		h.DB.Seed("contact_identifier_types", map[string]any{
			"id": typeID, "code": fmt.Sprintf("ref-%d", i), "label": "Reference",
		})
		h.DB.Seed("contact_identifiers", map[string]any{
			"id": typeID, "contact_id": id, "type_id": typeID, "value": "MATCH",
		})
	}
	requireDuplicateReasons(t, duplicateMatches(t, h.POST(duplicatesPath, map[string]any{
		"type": "person", "identifiers": []map[string]any{{"type_id": id, "value": "MATCH"}},
	})), id, "identifier")
}

func TestDuplicatesRejectsInvalidCandidates(t *testing.T) {
	h := modeltest.NewHarness(t)
	for _, body := range []map[string]any{
		{},
		{"type": "company"},
		{"type": "person", "exclude_id": "invalid"},
		{"type": "person", "identifiers": []map[string]any{{"type_id": "invalid", "value": "123"}}},
	} {
		response := h.POST(duplicatesPath, body)
		if response.StatusCode != http.StatusBadRequest {
			t.Errorf("body=%v status=%d error=%v, want 400", body, response.StatusCode, response.JSON("error"))
		}
	}
}
