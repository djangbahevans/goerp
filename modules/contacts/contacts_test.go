package contacts_test

import (
	"net/http"
	"testing"

	"github.com/djangbahevans/goerp/sdk/go/modeltest"
)

const contactsPath = "/contacts/contacts"

func createContact(t *testing.T, h *modeltest.Harness, body map[string]any) string {
	t.Helper()
	resp := h.POST(contactsPath, body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create %v: status = %d, error = %v %v", body, resp.StatusCode, resp.JSON("error.code"), resp.JSON("error.message"))
	}
	id, _ := resp.JSON("id").(string)
	if id == "" {
		t.Fatalf("create %v: response has no id", body)
	}
	return id
}

func requireRejected(t *testing.T, resp *modeltest.Response, message string) {
	t.Helper()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (error = %v %v)", resp.StatusCode, resp.JSON("error.code"), resp.JSON("error.message"))
	}
	if got := resp.ErrorCode(); got != "orm.validation_failed" {
		t.Errorf("error code = %q, want orm.validation_failed", got)
	}
	if got := resp.JSON("error.message"); got != message {
		t.Errorf("error message = %v, want %q", got, message)
	}
}

const (
	onlyPersonMessage     = "only a person can belong to a company"
	activeCompanyMessage  = "must reference an active company"
	selfLinkMessage       = "a contact cannot belong to itself"
	referencedTypeMessage = "a company that people belong to cannot become a person"
)

func TestContactDefaultsAndStandaloneCRUD(t *testing.T) {
	h := modeltest.NewHarness(t)

	id := createContact(t, h, map[string]any{"name": "Acme Ltd"})

	got := h.GET(contactsPath + "/" + id)
	if got.StatusCode != http.StatusOK {
		t.Fatalf("get: status = %d", got.StatusCode)
	}
	for field, want := range map[string]any{
		"type":         "company",
		"name":         "Acme Ltd",
		"display_name": "Acme Ltd",
		"is_customer":  false,
		"is_supplier":  false,
		"is_active":    true,
	} {
		if v := got.JSON(field); v != want {
			t.Errorf("%s = %v, want %v", field, v, want)
		}
	}

	if list := h.GET(contactsPath); list.StatusCode != http.StatusOK || len(list.JSONArray("data")) != 1 {
		t.Fatalf("list: status = %d, rows = %d, want 200 and 1", list.StatusCode, len(list.JSONArray("data")))
	}

	etag, _ := got.JSON("etag").(string)
	update := h.PUT(contactsPath+"/"+id, map[string]any{"city": "Accra", "is_supplier": true, "etag": etag})
	if update.StatusCode != http.StatusOK {
		t.Fatalf("update: status = %d, error = %v %v", update.StatusCode, update.JSON("error.code"), update.JSON("error.message"))
	}
	if update.JSON("city") != "Accra" || update.JSON("is_supplier") != true {
		t.Errorf("update response = city %v, is_supplier %v", update.JSON("city"), update.JSON("is_supplier"))
	}
}

func TestDeleteArchivesTheContact(t *testing.T) {
	h := modeltest.NewHarness(t)
	id := createContact(t, h, map[string]any{"name": "Acme Ltd"})

	if resp := h.DELETE(contactsPath + "/" + id); resp.StatusCode >= 400 {
		t.Fatalf("delete: status = %d, error = %v %v", resp.StatusCode, resp.JSON("error.code"), resp.JSON("error.message"))
	}

	h.DB.AssertCount("contacts", 1, "id = '"+id+"' AND deleted_at IS NOT NULL")
	if resp := h.GET(contactsPath + "/" + id); resp.StatusCode != http.StatusNotFound {
		t.Errorf("get archived: status = %d, want 404", resp.StatusCode)
	}
}

func TestOnlyAPersonCanHaveACompany(t *testing.T) {
	h := modeltest.NewHarness(t)
	company := createContact(t, h, map[string]any{"name": "Acme Ltd"})

	person := createContact(t, h, map[string]any{"type": "person", "name": "Ama Mensah", "company_id": company})
	h.DB.AssertCount("contacts", 1, "id = '"+person+"' AND company_id = '"+company+"'")

	requireRejected(t, h.POST(contactsPath, map[string]any{"type": "company", "name": "Sub Ltd", "company_id": company}), onlyPersonMessage)
}

func TestCompanyLinkMustTargetAnActiveCompany(t *testing.T) {
	h := modeltest.NewHarness(t)
	otherPerson := createContact(t, h, map[string]any{"type": "person", "name": "Kofi Boateng"})
	inactive := createContact(t, h, map[string]any{"name": "Dormant Ltd", "is_active": false})
	archived := createContact(t, h, map[string]any{"name": "Closed Ltd"})
	if resp := h.DELETE(contactsPath + "/" + archived); resp.StatusCode >= 400 {
		t.Fatalf("archive company: status = %d", resp.StatusCode)
	}

	for name, target := range map[string]string{
		"a person":            otherPerson,
		"an inactive company": inactive,
		"an archived company": archived,
	} {
		t.Run(name, func(t *testing.T) {
			requireRejected(t, h.POST(contactsPath, map[string]any{"type": "person", "name": "Ama Mensah", "company_id": target}), activeCompanyMessage)
		})
	}
}

func TestCompanyLinkToAMissingContactIsRejected(t *testing.T) {
	h := modeltest.NewHarness(t)

	resp := h.POST(contactsPath, map[string]any{"type": "person", "name": "Ama Mensah", "company_id": "0198d6f5-0000-7000-8000-000000000000"})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (error = %v %v)", resp.StatusCode, resp.JSON("error.code"), resp.JSON("error.message"))
	}
	h.DB.AssertCount("contacts", 0, "")
}

func TestSelfLinkIsRejected(t *testing.T) {
	h := modeltest.NewHarness(t)
	id := createContact(t, h, map[string]any{"type": "person", "name": "Ama Mensah"})
	etag, _ := h.GET(contactsPath + "/" + id).JSON("etag").(string)

	requireRejected(t, h.PUT(contactsPath+"/"+id, map[string]any{"company_id": id, "etag": etag}), selfLinkMessage)
}

func TestReferencedCompanyCannotBecomeAPerson(t *testing.T) {
	h := modeltest.NewHarness(t)
	company := createContact(t, h, map[string]any{"name": "Acme Ltd"})
	createContact(t, h, map[string]any{"type": "person", "name": "Ama Mensah", "company_id": company})
	etag, _ := h.GET(contactsPath + "/" + company).JSON("etag").(string)

	requireRejected(t, h.PUT(contactsPath+"/"+company, map[string]any{"type": "person", "etag": etag}), referencedTypeMessage)

	unreferenced := createContact(t, h, map[string]any{"name": "Solo Ltd"})
	etag, _ = h.GET(contactsPath + "/" + unreferenced).JSON("etag").(string)
	if resp := h.PUT(contactsPath+"/"+unreferenced, map[string]any{"type": "person", "etag": etag}); resp.StatusCode != http.StatusOK {
		t.Errorf("unreferenced company -> person: status = %d, error = %v %v", resp.StatusCode, resp.JSON("error.code"), resp.JSON("error.message"))
	}
}

func TestArchivedCompanyKeepsExistingLinksButRejectsNewOnes(t *testing.T) {
	h := modeltest.NewHarness(t)
	company := createContact(t, h, map[string]any{"name": "Acme Ltd"})
	person := createContact(t, h, map[string]any{"type": "person", "name": "Ama Mensah", "company_id": company})

	if resp := h.DELETE(contactsPath + "/" + company); resp.StatusCode >= 400 {
		t.Fatalf("archive company: status = %d", resp.StatusCode)
	}

	got := h.GET(contactsPath + "/" + person)
	etag, _ := got.JSON("etag").(string)
	if got.JSON("company_id") != company {
		t.Fatalf("company_id after archive = %v, want %s", got.JSON("company_id"), company)
	}
	if resp := h.PUT(contactsPath+"/"+person, map[string]any{"city": "Kumasi", "etag": etag}); resp.StatusCode != http.StatusOK {
		t.Fatalf("edit person of archived company: status = %d, error = %v %v", resp.StatusCode, resp.JSON("error.code"), resp.JSON("error.message"))
	}

	newPerson := h.POST(contactsPath, map[string]any{"type": "person", "name": "Yaw Asante", "company_id": company})
	requireRejected(t, newPerson, activeCompanyMessage)
}

func TestDocumentedIndexesExist(t *testing.T) {
	h := modeltest.NewHarness(t)

	for _, name := range []string{
		"idx_contacts_name",
		"idx_contacts_phone",
		"idx_contacts_is_customer",
		"idx_contacts_is_supplier",
		"idx_contacts_company",
	} {
		h.DB.AssertIndexExists(name)
	}
}

func displayName(t *testing.T, h *modeltest.Harness, id string) any {
	t.Helper()
	resp := h.GET(contactsPath + "/" + id)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get %s: status = %d", id, resp.StatusCode)
	}
	return resp.JSON("display_name")
}

func requireDisplayName(t *testing.T, h *modeltest.Harness, id, want string) {
	t.Helper()
	if got := displayName(t, h, id); got != want {
		t.Errorf("display_name of %s = %v, want %q", id, got, want)
	}
}

func TestDisplayNameIncludesTheCompanyOfAPerson(t *testing.T) {
	h := modeltest.NewHarness(t)
	company := createContact(t, h, map[string]any{"name": "Acme Ltd"})
	linked := createContact(t, h, map[string]any{"type": "person", "name": "Ama Mensah", "company_id": company})
	standalone := createContact(t, h, map[string]any{"type": "person", "name": "Kofi Boateng"})

	requireDisplayName(t, h, company, "Acme Ltd")
	requireDisplayName(t, h, linked, "Ama Mensah (Acme Ltd)")
	requireDisplayName(t, h, standalone, "Kofi Boateng")
}

func TestDisplayNameFollowsCompanyAndPersonChanges(t *testing.T) {
	h := modeltest.NewHarness(t)
	acme := createContact(t, h, map[string]any{"name": "Acme Ltd"})
	globex := createContact(t, h, map[string]any{"name": "Globex Ltd"})
	first := createContact(t, h, map[string]any{"type": "person", "name": "Ama Mensah", "company_id": acme})
	second := createContact(t, h, map[string]any{"type": "person", "name": "Kofi Boateng", "company_id": acme})
	other := createContact(t, h, map[string]any{"type": "person", "name": "Esi Owusu", "company_id": globex})

	if resp := h.PUT(contactsPath+"/"+acme, map[string]any{"name": "Acme Holdings"}); resp.StatusCode != http.StatusOK {
		t.Fatalf("rename company: status = %d, error = %v", resp.StatusCode, resp.JSON("error.message"))
	}
	requireDisplayName(t, h, acme, "Acme Holdings")
	requireDisplayName(t, h, first, "Ama Mensah (Acme Holdings)")
	requireDisplayName(t, h, second, "Kofi Boateng (Acme Holdings)")
	requireDisplayName(t, h, other, "Esi Owusu (Globex Ltd)")

	if resp := h.PUT(contactsPath+"/"+first, map[string]any{"name": "Ama Asante"}); resp.StatusCode != http.StatusOK {
		t.Fatalf("rename person: status = %d, error = %v", resp.StatusCode, resp.JSON("error.message"))
	}
	requireDisplayName(t, h, first, "Ama Asante (Acme Holdings)")

	if resp := h.PUT(contactsPath+"/"+first, map[string]any{"company_id": globex}); resp.StatusCode != http.StatusOK {
		t.Fatalf("move person: status = %d, error = %v", resp.StatusCode, resp.JSON("error.message"))
	}
	requireDisplayName(t, h, first, "Ama Asante (Globex Ltd)")

	if resp := h.PUT(contactsPath+"/"+first, map[string]any{"company_id": nil}); resp.StatusCode != http.StatusOK {
		t.Fatalf("unlink person: status = %d, error = %v", resp.StatusCode, resp.JSON("error.message"))
	}
	requireDisplayName(t, h, first, "Ama Asante")
}

func TestDisplayNameKeepsAnArchivedCompanyName(t *testing.T) {
	h := modeltest.NewHarness(t)
	company := createContact(t, h, map[string]any{"name": "Closed Ltd"})
	person := createContact(t, h, map[string]any{"type": "person", "name": "Ama Mensah", "company_id": company})
	if resp := h.DELETE(contactsPath + "/" + company); resp.StatusCode >= 400 {
		t.Fatalf("archive company: status = %d", resp.StatusCode)
	}

	if resp := h.PUT(contactsPath+"/"+person, map[string]any{"name": "Ama Asante"}); resp.StatusCode != http.StatusOK {
		t.Fatalf("rename person: status = %d, error = %v", resp.StatusCode, resp.JSON("error.message"))
	}
	requireDisplayName(t, h, person, "Ama Asante (Closed Ltd)")
}
