package contacts_test

import (
	"net/http"
	"testing"
	"uuid"

	"github.com/djangbahevans/goerp/modules/contacts/schema"
	"github.com/djangbahevans/goerp/sdk/go/modeltest"
)

const tagsPath = "/contacts/tags"

func createTag(t *testing.T, h *modeltest.Harness, name string) string {
	t.Helper()
	resp := h.POST(tagsPath, map[string]any{"name": name, "color": "#123456"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create tag %q: status = %d, error = %v", name, resp.StatusCode, resp.JSON("error"))
	}
	return resp.JSON("id").(string)
}

func TestTagNamesAreUniqueWithinEachTenantIgnoringCase(t *testing.T) {
	h := modeltest.NewHarness(t)
	id := createTag(t, h, "Key Account")
	h.DB.AssertIndexExists("idx_contact_tags_name")

	resp := h.POST(tagsPath, map[string]any{"name": "KEY account"})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("duplicate tag: status = %d, error = %v", resp.StatusCode, resp.JSON("error"))
	}

	other := createTag(t, h, "PTA committee")
	if resp := h.PUT(tagsPath+"/"+other, map[string]any{"name": "key ACCOUNT"}); resp.StatusCode != http.StatusConflict {
		t.Fatalf("duplicate rename: status = %d, error = %v", resp.StatusCode, resp.JSON("error"))
	}

	otherTenant := modeltest.NewHarness(t)
	foreignTag := createTag(t, otherTenant, "KEY ACCOUNT")
	if resp := h.POST(contactsPath, map[string]any{"type": "person", "given_name": "Ama", "tag_ids": []string{foreignTag}}); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("foreign tag assignment: %d %v", resp.StatusCode, resp.JSON("error"))
	}
	requireNames(t, searchNames(t, h, modeltest.WithQuery("tag", foreignTag)))
	if resp := h.DELETE(tagsPath + "/" + id); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("archive tag: %d %v", resp.StatusCode, resp.JSON("error"))
	}
	createTag(t, h, "key account")
}

func TestTagsFilterContactsAndSearch(t *testing.T) {
	h := modeltest.NewHarness(t)
	tag := createTag(t, h, "Key account")
	secondTag := createTag(t, h, "PTA committee")
	for _, body := range []map[string]any{
		{"type": "organization", "legal_name": "Acme", "tag_ids": []string{tag, secondTag}},
		{"type": "organization", "legal_name": "Acme Trading", "tag_ids": []string{tag}},
		{"type": "organization", "legal_name": "The Acme Shop", "tag_ids": []string{tag}},
		{"type": "person", "given_name": "Acme", "tag_ids": []string{secondTag}},
		{"type": "organization", "legal_name": "Acme Untagged"},
	} {
		createContact(t, h, body)
	}

	requireNames(t, searchNames(t, h, modeltest.WithQuery("tag", tag)), "Acme", "Acme Trading", "The Acme Shop")
	requireNames(t, searchNames(t, h, modeltest.WithQuery("tag", tag), modeltest.WithQuery("q", "acme")), "Acme", "Acme Trading", "The Acme Shop")
	requireNames(t, searchNames(t, h, modeltest.WithQuery("tag", secondTag), modeltest.WithQuery("type", "person")), "Acme")
	requireNames(t, searchNames(t, h, modeltest.WithQuery("tag", uuid.NewV7().String())))
	if resp := h.GET(searchPath, modeltest.WithQuery("tag", "invalid' OR true")); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("invalid tag: status = %d, want 400", resp.StatusCode)
	}

	resp := h.GET(contactsPath, modeltest.WithQuery("filter[tag_ids]", tag))
	if resp.StatusCode != http.StatusOK || len(resp.JSONArray("data")) != 3 {
		t.Fatalf("list tag filter: status = %d, data = %v, error = %v", resp.StatusCode, resp.JSON("data"), resp.JSON("error"))
	}
	resp = h.GET(contactsPath, modeltest.WithQuery("filter[tag_ids][in]", tag+","+secondTag))
	if resp.StatusCode != http.StatusOK || len(resp.JSONArray("data")) != 4 {
		t.Fatalf("list tag union: status = %d, data = %v, error = %v", resp.StatusCode, resp.JSON("data"), resp.JSON("error"))
	}
}

func TestArchivingTagRetainsAssignmentsAndAllowsContactEdits(t *testing.T) {
	h := modeltest.NewHarness(t)
	archived := createTag(t, h, "PTA committee")
	active := createTag(t, h, "Key account")
	contact := createContact(t, h, map[string]any{"type": "person", "given_name": "Ama", "tag_ids": []string{archived}})
	if resp := h.DELETE(tagsPath + "/" + archived); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("archive: %d", resp.StatusCode)
	}

	for _, resp := range []*modeltest.Response{
		h.GET(contactsPath + "/" + contact),
		h.PUT(contactsPath+"/"+contact, map[string]any{"notes": "Still a committee member", "tag_ids": []string{archived, active}}),
	} {
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("contact: %d %v", resp.StatusCode, resp.JSON("error"))
		}
		found := false
		for _, tag := range resp.JSONArray("tags") {
			if tag["id"] == archived && tag["name"] == "PTA committee" {
				found = true
			}
		}
		if !found {
			t.Errorf("archived assignment missing: %v", resp.JSON("tags"))
		}
	}

	resp := h.GET(tagsPath)
	if resp.StatusCode != http.StatusOK || len(resp.JSONArray("data")) != 1 || resp.JSONArray("data")[0]["id"] != active {
		t.Fatalf("active tag choices: status = %d, data = %v", resp.StatusCode, resp.JSON("data"))
	}
	requireNames(t, searchNames(t, h, modeltest.WithQuery("tag", archived)))
	resp = h.GET(contactsPath, modeltest.WithQuery("filter[tag_ids]", archived))
	if resp.StatusCode != http.StatusOK || len(resp.JSONArray("data")) != 0 {
		t.Fatalf("archived filter: %d %v", resp.StatusCode, resp.JSON("error"))
	}

	other := createContact(t, h, map[string]any{"type": "person", "given_name": "Esi"})
	for _, resp := range []*modeltest.Response{
		h.PUT(contactsPath+"/"+other, map[string]any{"tag_ids": []string{archived}}),
		h.POST(contactsPath, map[string]any{"type": "person", "given_name": "Kofi", "tag_ids": []string{archived}}),
	} {
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("new archived assignment: %d %v", resp.StatusCode, resp.JSON("error"))
		}
	}

	if resp := h.PUT(contactsPath+"/"+contact, map[string]any{"tag_ids": []string{active}}); resp.StatusCode != http.StatusOK {
		t.Fatalf("remove archived: %d", resp.StatusCode)
	}
	if resp := h.PUT(contactsPath+"/"+contact, map[string]any{"tag_ids": []string{archived, active}}); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("reattach archived: %d", resp.StatusCode)
	}
}

func TestTagPermissionsSeparateConfigurationFromAssignment(t *testing.T) {
	h := modeltest.NewHarness(t)
	tag := createTag(t, h, "Key account")
	contact := createContact(t, h, map[string]any{"type": "person", "given_name": "Ama"})
	for _, resp := range []*modeltest.Response{
		h.WithPermissions().GET(tagsPath),
		h.WithPermissions().GET(tagsPath + "/" + tag),
		h.WithPermissions(schema.ContactWrite).POST(tagsPath, map[string]any{"name": "No permission"}),
		h.WithPermissions(schema.ContactWrite).PUT(tagsPath+"/"+tag, map[string]any{"name": "No permission"}),
		h.WithPermissions(schema.ContactWrite).DELETE(tagsPath + "/" + tag),
		h.WithPermissions(schema.ContactWrite).POST(tagsPath+"/preview", map[string]any{"name": "No permission"}),
		h.WithPermissions(schema.ContactRead).PUT(contactsPath+"/"+contact, map[string]any{"tag_ids": []string{tag}}),
	} {
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("restricted tag operation: %d %v", resp.StatusCode, resp.JSON("error"))
		}
	}
	for _, resp := range []*modeltest.Response{
		h.WithPermissions(schema.ContactRead).GET(tagsPath),
		h.WithPermissions(schema.ContactRead).GET(tagsPath + "/" + tag),
		h.WithPermissions(schema.ContactWrite).PUT(contactsPath+"/"+contact, map[string]any{"tag_ids": []string{tag}}),
		h.WithPermissions(schema.ConfigWrite).PUT(tagsPath+"/"+tag, map[string]any{"color": "#abcdef"}),
	} {
		if resp.StatusCode != http.StatusOK {
			t.Errorf("authorized tag operation: %d %v", resp.StatusCode, resp.JSON("error"))
		}
	}
}
