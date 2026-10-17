package contacts_test

import (
	"maps"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/modules/contacts/schema"
	"github.com/djangbahevans/goerp/sdk/go/modeltest"
)

const affiliationsPath = "/contacts/affiliations"

func createAffiliation(t *testing.T, h *modeltest.Harness, person, organization string, extra map[string]any) string {
	t.Helper()

	body := map[string]any{"person_id": person, "organization_id": organization}
	maps.Copy(body, extra)
	response := h.POST(affiliationsPath, body)
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("create affiliation: status=%d error=%v", response.StatusCode, response.JSON("error"))
	}
	return response.JSON("id").(string)
}

func assertPrimaryAffiliation(t *testing.T, h *modeltest.Harness, person, affiliation string) {
	t.Helper()

	var result struct {
		ID    string
		Count int
	}
	h.DB.QueryOne(&result, `SELECT coalesce(min(id::text), '') AS id, count(*) AS count FROM contact_affiliations WHERE person_id=$1 AND is_primary AND deleted_at IS NULL`, person)
	want := 0
	if affiliation != "" {
		want = 1
	}
	if result.Count != want || result.ID != affiliation {
		t.Errorf("primary=%s count=%d, want %s count=%d", result.ID, result.Count, affiliation, want)
	}
}

func TestAffiliationPrimariesMovesAndHistory(t *testing.T) {
	h := modeltest.NewHarness(t)
	person := createContact(t, h, map[string]any{"type": "person", "given_name": "Ama"})
	other := createContact(t, h, map[string]any{"type": "person", "given_name": "Esi"})
	organization := createContact(t, h, map[string]any{"type": "organization", "legal_name": "Acme"})
	first := createAffiliation(t, h, person, organization, map[string]any{"job_title": "Consultant", "department": "Engineering", "start_date": "2020-01-01"})
	second := createAffiliation(t, h, person, organization, nil)
	assertPrimaryAffiliation(t, h, person, first)
	row := h.GET(affiliationsPath + "/" + first)
	if row.JSON("job_title") != "Consultant" || row.JSON("department") != "Engineering" {
		t.Fatalf("affiliation=%v", row)
	}
	for _, step := range []struct {
		id      string
		body    map[string]any
		primary string
	}{
		{second, map[string]any{"is_primary": true}, second},
		{second, map[string]any{"is_primary": false}, first},
		{first, map[string]any{"person_id": other}, second},
		{first, map[string]any{"person_id": person}, first},
		{first, map[string]any{"end_date": "2021-01-01"}, second},
	} {
		response := h.PUT(affiliationsPath+"/"+step.id, step.body)
		if response.StatusCode != 200 {
			t.Fatalf("update %v: status=%d error=%v", step.body, response.StatusCode, response.JSON("error"))
		}
		assertPrimaryAffiliation(t, h, person, step.primary)
	}
	assertPrimaryAffiliation(t, h, other, "")
	if response := h.DELETE(affiliationsPath + "/" + second); response.StatusCode != 204 {
		t.Fatalf("archive: %v", response.JSON("error"))
	}
	assertPrimaryAffiliation(t, h, person, "")
	h.DB.AssertIndexExists("idx_contact_affiliations_primary")
	h.DB.AssertIndexExists("idx_contact_affiliations_organization")
}

func TestAffiliationValidationAndRollback(t *testing.T) {
	h := modeltest.NewHarness(t)
	person := createContact(t, h, map[string]any{"type": "person", "given_name": "Ama"})
	organization := createContact(t, h, map[string]any{"type": "organization", "legal_name": "Acme"})
	inactive := createContact(t, h, map[string]any{"type": "organization", "legal_name": "Inactive", "is_active": false})
	first := createAffiliation(t, h, person, organization, nil)
	second := createAffiliation(t, h, person, organization, nil)
	for _, body := range []map[string]any{
		{"organization_id": person}, {"person_id": organization}, {"organization_id": inactive},
		{"person_id": nil}, {"organization_id": "invalid"}, {"is_primary": "yes"},
		{"start_date": "2099-02-01", "end_date": "2099-01-01"},
		{"end_date": "2020-01-01", "is_primary": true}, {"end_date": "invalid"},
		{"unknown": "value"}, {"created_at": "2020-01-01T00:00:00Z"},
	} {
		if _, supplied := body["is_primary"]; !supplied {
			body["is_primary"] = true
		}
		response := h.PUT(affiliationsPath+"/"+second, body)
		if response.StatusCode != 400 {
			t.Errorf("invalid %v: status=%d error=%v", body, response.StatusCode, response.JSON("error"))
		}
		assertPrimaryAffiliation(t, h, person, first)
	}
	response := h.WithHeader("If-Match", "stale").PUT(affiliationsPath+"/"+second, map[string]any{"is_primary": true})
	if response.StatusCode != 409 {
		t.Errorf("stale etag: status=%d error=%v", response.StatusCode, response.JSON("error"))
	}
	assertPrimaryAffiliation(t, h, person, first)
	for _, step := range []struct {
		id   string
		body map[string]any
	}{
		{person, map[string]any{"type": "organization", "given_name": nil, "legal_name": "Changed"}},
		{organization, map[string]any{"type": "person", "legal_name": nil, "given_name": "Changed"}},
	} {
		response := h.PUT(contactsPath+"/"+step.id, step.body)
		if response.StatusCode != 400 || response.JSON("error.details.field") != "type" {
			t.Errorf("kind change: status=%d error=%v", response.StatusCode, response.JSON("error"))
		}
	}
}

func TestAffiliationRetainsArchivedAndInactiveParties(t *testing.T) {
	h := modeltest.NewHarness(t)
	person := createContact(t, h, map[string]any{"type": "person", "given_name": "Ama"})
	organization := createContact(t, h, map[string]any{"type": "organization", "legal_name": "Acme"})
	id := createAffiliation(t, h, person, organization, nil)
	if response := h.PUT(contactsPath+"/"+organization, map[string]any{"is_active": false}); response.StatusCode != 200 {
		t.Fatal(response.JSON("error"))
	}
	if response := h.POST(affiliationsPath, map[string]any{"person_id": person, "organization_id": organization}); response.StatusCode != 400 {
		t.Errorf("inactive target: status=%d", response.StatusCode)
	}
	if response := h.DELETE(contactsPath + "/" + organization); response.StatusCode != 204 {
		t.Fatal(response.JSON("error"))
	}
	response := h.PUT(affiliationsPath+"/"+id, map[string]any{"organization_id": strings.ToUpper(organization), "job_title": "Board member"})
	if response.StatusCode != 200 || response.JSON("organization_id") != organization {
		t.Fatalf("retain archived: status=%d error=%v", response.StatusCode, response.JSON("error"))
	}
	if response := h.POST(affiliationsPath, map[string]any{"person_id": person, "organization_id": organization}); response.StatusCode != 404 {
		t.Errorf("archived target: status=%d error=%v", response.StatusCode, response.JSON("error"))
	}
	if response := h.DELETE(affiliationsPath + "/" + id); response.StatusCode != 204 {
		t.Fatalf("archive retained: status=%d error=%v", response.StatusCode, response.JSON("error"))
	}
}

func TestAffiliationPermissionsAndTenantIsolation(t *testing.T) {
	h := modeltest.NewHarness(t)
	person := createContact(t, h, map[string]any{"type": "person", "given_name": "Ama"})
	organization := createContact(t, h, map[string]any{"type": "organization", "legal_name": "Acme"})
	id := createAffiliation(t, h, person, organization, nil)
	reader := h.WithPermissions(schema.ContactRead)
	if response := reader.GET(affiliationsPath + "/" + id); response.StatusCode != 200 {
		t.Fatalf("read: %d", response.StatusCode)
	}
	for _, response := range []*modeltest.Response{
		reader.POST(affiliationsPath, map[string]any{"person_id": person, "organization_id": organization}),
		reader.PUT(affiliationsPath+"/"+id, map[string]any{"job_title": "Unauthorized"}),
		reader.DELETE(affiliationsPath + "/" + id),
	} {
		if response.StatusCode != 403 {
			t.Errorf("reader mutation: %d", response.StatusCode)
		}
	}
	other := modeltest.NewHarness(t)
	foreign := createContact(t, other, map[string]any{"type": "organization", "legal_name": "Other tenant"})
	if response := h.POST(affiliationsPath, map[string]any{"person_id": person, "organization_id": foreign}); response.StatusCode != 404 {
		t.Errorf("foreign target: status=%d error=%v", response.StatusCode, response.JSON("error"))
	}
}

func TestConcurrentFirstAffiliationsHaveOnePrimary(t *testing.T) {
	h := modeltest.NewHarness(t)
	person := createContact(t, h, map[string]any{"type": "person", "given_name": "Ama"})
	organization := createContact(t, h, map[string]any{"type": "organization", "legal_name": "Acme"})
	var responses [2]*modeltest.Response
	var wg sync.WaitGroup
	for i := range 2 {
		wg.Go(func() {
			personID := person
			if i == 1 {
				personID = strings.ToUpper(personID)
			}
			responses[i] = h.POST(affiliationsPath, map[string]any{"person_id": personID, "organization_id": organization})
		})
	}
	wg.Wait()
	for _, response := range responses {
		if response.StatusCode != 201 {
			t.Fatalf("concurrent create: status=%d error=%v", response.StatusCode, response.JSON("error"))
		}
	}
	h.DB.AssertCount("contact_affiliations", 1, "is_primary AND deleted_at IS NULL")
	h.DB.AssertCount("contact_affiliations", 2, "deleted_at IS NULL")
}

func TestSearchPrimaryOrganizationExpiresWithClock(t *testing.T) {
	var clock atomic.Int64
	clock.Store(time.Date(2026, 1, 10, 12, 0, 0, 0, time.UTC).Unix())
	h := modeltest.NewHarness(t, modeltest.WithClock(func() time.Time { return time.Unix(clock.Load(), 0) }))
	person := createContact(t, h, map[string]any{"type": "person", "given_name": "Ama"})
	organization := createContact(t, h, map[string]any{"type": "organization", "legal_name": "Acme"})
	createAffiliation(t, h, person, organization, map[string]any{"end_date": "2026-01-10"})
	response := h.GET(searchPath, modeltest.WithQuery("q", "Ama"))
	items := response.JSONArray("items")
	if response.StatusCode != 200 || len(items) != 1 {
		t.Fatalf("search: status=%d items=%v", response.StatusCode, items)
	}
	primary, ok := items[0]["organization"].(map[string]any)
	if !ok || primary["id"] != organization || primary["display_name"] != "Acme" {
		t.Fatalf("primary organization: %v", items)
	}
	clock.Store(time.Date(2026, 1, 11, 0, 0, 0, 0, time.UTC).Unix())
	response = h.GET(searchPath, modeltest.WithQuery("q", "Ama"))
	items = response.JSONArray("items")
	if response.StatusCode != 200 || len(items) != 1 || items[0]["organization"] != nil {
		t.Fatalf("ended organization: status=%d items=%v", response.StatusCode, items)
	}
	replacement := createAffiliation(t, h, person, organization, nil)
	assertPrimaryAffiliation(t, h, person, replacement)
}
