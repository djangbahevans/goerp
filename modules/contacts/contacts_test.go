package contacts_test

import (
	"cmp"
	"net/http"
	"os"
	"path/filepath"
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

func TestPartyCRUD(t *testing.T) {
	h := modeltest.NewHarness(t)
	for _, body := range []map[string]any{
		{"type": "person", "given_name": "Ama", "family_name": "Mensah"},
		{"type": "organization", "legal_name": "Acme Ltd"},
	} {
		id := createContact(t, h, body)
		got := h.GET(contactsPath + "/" + id)
		if got.StatusCode != http.StatusOK || got.JSON("is_active") != true {
			t.Fatalf("get: status = %d, active = %v", got.StatusCode, got.JSON("is_active"))
		}

		update := h.PUT(contactsPath+"/"+id, map[string]any{
			"notes": "Directory entry", "language": "en", "preferred_channel": "sms", "is_active": false,
		})
		if update.StatusCode != http.StatusOK || update.JSON("notes") != "Directory entry" || update.JSON("is_active") != false {
			t.Fatalf("update: status = %d, error = %v", update.StatusCode, update.JSON("error"))
		}
		if resp := h.DELETE(contactsPath + "/" + id); resp.StatusCode != http.StatusNoContent {
			t.Fatalf("archive: status = %d", resp.StatusCode)
		}
		h.DB.AssertCount("contacts", 1, "id = '"+id+"' AND deleted_at IS NOT NULL")
		if resp := h.GET(contactsPath + "/" + id); resp.StatusCode != http.StatusNotFound {
			t.Errorf("get archived: status = %d, want 404", resp.StatusCode)
		}
	}
	if list := h.GET(contactsPath); list.StatusCode != http.StatusOK || len(list.JSONArray("data")) != 0 {
		t.Fatalf("list: status = %d, rows = %d", list.StatusCode, len(list.JSONArray("data")))
	}
}

func TestNameDerivations(t *testing.T) {
	h := modeltest.NewHarness(t)
	for _, tc := range []struct {
		given, other, family, preferred string
		full, display, sort             string
	}{
		{"Ama", "", "", "", "Ama", "Ama", "Ama"},
		{"Ama", "Akua", "", "", "Ama Akua", "Ama", "Ama Akua"},
		{"", "", "Mensah", "", "Mensah", "Mensah", "Mensah"},
		{"Ama", "", "Mensah", "", "Ama Mensah", "Ama Mensah", "Mensah, Ama"},
		{"", "Akua", "Mensah", "", "Akua Mensah", "Mensah", "Mensah, Akua"},
		{"Ama", "Akua", "Mensah", "", "Ama Akua Mensah", "Ama Mensah", "Mensah, Ama Akua"},
		{"Ama", "", "", "Mimi", "Ama", "Mimi", "Ama"},
		{"Ama", "Akua", "", "Mimi", "Ama Akua", "Mimi", "Ama Akua"},
		{"", "", "Mensah", "Mimi", "Mensah", "Mimi Mensah", "Mensah"},
		{"Ama", "", "Mensah", "Mimi", "Ama Mensah", "Mimi Mensah", "Mensah, Ama"},
		{"", "Akua", "Mensah", "Mimi", "Akua Mensah", "Mimi Mensah", "Mensah, Akua"},
		{"Ama", "Akua", "Mensah", "Mimi", "Ama Akua Mensah", "Mimi Mensah", "Mensah, Ama Akua"},
	} {
		t.Run(tc.sort+"_"+tc.preferred, func(t *testing.T) {
			id := createContact(t, h, map[string]any{
				"type": "person", "title": "Dr", "given_name": tc.given, "other_names": tc.other,
				"family_name": tc.family, "preferred_name": tc.preferred,
			})
			assertNames(t, h.GET(contactsPath+"/"+id), tc.full, tc.display, tc.sort)
		})
	}
	for _, trading := range []string{"", "Acme"} {
		id := createContact(t, h, map[string]any{"type": "organization", "legal_name": "Acme Limited", "trading_name": trading})
		display := cmp.Or(trading, "Acme Limited")
		assertNames(t, h.GET(contactsPath+"/"+id), "Acme Limited", display, display)
	}
}

func assertNames(t *testing.T, resp *modeltest.Response, full, display, sort string) {
	t.Helper()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, error = %v", resp.StatusCode, resp.JSON("error"))
	}
	for field, want := range map[string]string{"full_name": full, "display_name": display, "sort_name": sort} {
		if got := resp.JSON(field); got != want {
			t.Errorf("%s = %v, want %q", field, got, want)
		}
	}
}

func TestNamesRecomputeOnUpdateAndPreview(t *testing.T) {
	h := modeltest.NewHarness(t)
	id := createContact(t, h, map[string]any{"type": "person", "given_name": "Ama", "family_name": "Mensah"})
	resp := h.PUT(contactsPath+"/"+id, map[string]any{"other_names": "Akua", "preferred_name": "Mimi", "family_name": "Asante"})
	assertNames(t, resp, "Ama Akua Asante", "Mimi Asante", "Asante, Ama Akua")
	resp = h.PUT(contactsPath+"/"+id, map[string]any{"preferred_name": nil, "family_name": "", "given_name": "  Esi  "})
	assertNames(t, resp, "Esi Akua", "Esi", "Esi Akua")

	org := createContact(t, h, map[string]any{"type": "organization", "legal_name": "Acme Ltd"})
	assertNames(t, h.PUT(contactsPath+"/"+org, map[string]any{"trading_name": "Acme"}), "Acme Ltd", "Acme", "Acme")
	assertNames(t, h.PUT(contactsPath+"/"+org, map[string]any{"trading_name": "", "legal_name": "Acme Holdings"}), "Acme Holdings", "Acme Holdings", "Acme Holdings")
	assertNames(t, h.POST(contactsPath+"/preview", map[string]any{"type": "person", "given_name": "Ama", "family_name": "Mensah", "preferred_name": "Mimi", "other_names": nil, "legal_name": nil, "trading_name": nil}), "Ama Mensah", "Mimi Mensah", "Mensah, Ama")
}

func TestPartyNameConstraints(t *testing.T) {
	h := modeltest.NewHarness(t)
	for _, body := range []map[string]any{
		{"legal_name": "Missing type"},
		{"type": "company", "legal_name": "Invalid type"},
		{"type": "person", "preferred_name": "Mimi"},
		{"type": "person", "given_name": "   "},
		{"type": "organization", "trading_name": "Acme"},
	} {
		resp := h.POST(contactsPath, body)
		if resp.StatusCode != http.StatusBadRequest || resp.ErrorCode() != "orm.validation_failed" || resp.JSON("error.details.field") == nil {
			t.Errorf("create %v: status = %d, error = %v", body, resp.StatusCode, resp.JSON("error"))
		}
	}
	for _, field := range []string{"title", "given_name", "other_names", "family_name", "preferred_name", "date_of_birth", "gender"} {
		value := "person detail"
		if field == "date_of_birth" {
			value = "2000-01-01"
		}
		resp := h.POST(contactsPath, map[string]any{"type": "organization", "legal_name": "Acme Ltd", field: value})
		requireRejected(t, resp, "this field does not apply to this contact type")
	}
	for _, field := range []string{"legal_name", "trading_name"} {
		resp := h.POST(contactsPath, map[string]any{"type": "person", "family_name": "Mensah", field: "Acme Ltd"})
		requireRejected(t, resp, "this field does not apply to this contact type")
	}

	id := createContact(t, h, map[string]any{"type": "person", "given_name": "Ama"})
	requireRejected(t, h.PUT(contactsPath+"/"+id, map[string]any{"given_name": nil}), "a person needs a given name or family name")
	requireRejected(t, h.PUT(contactsPath+"/"+id, map[string]any{"type": "organization", "legal_name": "Acme"}), "this field does not apply to this contact type")
	assertNames(t, h.PUT(contactsPath+"/"+id, map[string]any{"type": "organization", "given_name": nil, "legal_name": "Acme"}), "Acme", "Acme", "Acme")
	requireRejected(t, h.PUT(contactsPath+"/"+id, map[string]any{"legal_name": ""}), "an organization needs a legal name")
	assertNames(t, h.GET(contactsPath+"/"+id), "Acme", "Acme", "Acme")
}

func TestPartySchema(t *testing.T) {
	h := modeltest.NewHarness(t)
	for _, name := range []string{"idx_contacts_full_name", "idx_contacts_sort_name"} {
		h.DB.AssertIndexExists(name)
	}
	for _, name := range []string{"idx_contacts_name", "idx_contacts_phone", "idx_contacts_is_customer", "idx_contacts_is_supplier", "idx_contacts_company"} {
		h.DB.AssertIndexNotExists(name)
	}
	var removed struct{ Count int }
	h.DB.QueryOne(&removed, `SELECT count(*) AS count FROM information_schema.columns WHERE table_schema = current_schema() AND table_name = 'contacts' AND column_name IN ('name', 'company_id', 'is_customer', 'is_supplier', 'mobile', 'street', 'city', 'country_code', 'tin')`)
	if removed.Count != 0 {
		t.Errorf("removed columns = %d", removed.Count)
	}

	fixture := filepath.Join(t.TempDir(), "avatar.png")
	if err := os.WriteFile(fixture, []byte("avatar fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	fileID := h.Storage.MockFile(fixture, modeltest.MockFileContentType("image/png"))
	id := createContact(t, h, map[string]any{"type": "person", "given_name": "Ama", "avatar_file_id": fileID})
	if got := h.GET(contactsPath + "/" + id).JSON("avatar_file.id"); got != fileID {
		t.Errorf("avatar id = %v, want %s", got, fileID)
	}
}

func TestDerivedAndRemovedFieldsRejectWrites(t *testing.T) {
	h := modeltest.NewHarness(t)
	id := createContact(t, h, map[string]any{"type": "organization", "legal_name": "Acme Ltd"})
	for _, field := range []string{
		"full_name", "display_name", "sort_name", "phone", "email",
		"name", "mobile", "street", "city", "country_code", "tin", "company_id", "is_customer", "is_supplier",
	} {
		for _, resp := range []*modeltest.Response{
			h.POST(contactsPath, map[string]any{"type": "organization", "legal_name": "Other Ltd", field: "override"}),
			h.PUT(contactsPath+"/"+id, map[string]any{field: "override"}),
		} {
			if resp.StatusCode != http.StatusBadRequest || resp.JSON("error.details.field") != field {
				t.Errorf("write %s: status = %d, error = %v", field, resp.StatusCode, resp.JSON("error"))
			}
		}
	}
	assertNames(t, h.GET(contactsPath+"/"+id), "Acme Ltd", "Acme Ltd", "Acme Ltd")
}
