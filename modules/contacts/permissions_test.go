package contacts_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/djangbahevans/goerp/modules/contacts/schema"
	"github.com/djangbahevans/goerp/sdk/go/modeltest"
)

func TestContactOperationPermissions(t *testing.T) {
	h := modeltest.NewHarness(t)
	id := createContact(t, h, map[string]any{"type": "organization", "legal_name": "Permission Test"})
	body := map[string]any{"type": "organization", "legal_name": "Restricted Contact"}

	for _, resp := range []*modeltest.Response{
		h.WithPermissions().GET(contactsPath),
		h.WithPermissions().GET(contactsPath + "/" + id),
		h.WithPermissions(schema.ContactRead).POST(contactsPath, body),
		h.WithPermissions(schema.ContactRead).PUT(contactsPath+"/"+id, body),
		h.WithPermissions(schema.ContactRead).DELETE(contactsPath + "/" + id),
		h.WithPermissions(schema.ContactRead).POST(contactsPath+"/preview", body),
	} {
		if resp.StatusCode != http.StatusForbidden || resp.ErrorCode() != "permission_denied" {
			t.Errorf("restricted request status = %d, error = %q, want 403 permission_denied", resp.StatusCode, resp.ErrorCode())
		}
	}

	for _, resp := range []*modeltest.Response{
		h.WithPermissions(schema.ContactRead).GET(contactsPath),
		h.WithPermissions(schema.ContactRead).GET(contactsPath + "/" + id),
		h.WithPermissions(schema.ContactWrite).POST(contactsPath+"/preview", body),
		h.WithPermissions(schema.ContactWrite).PUT(contactsPath+"/"+id, body),
	} {
		if resp.StatusCode != http.StatusOK {
			t.Errorf("authorized request status = %d, want 200", resp.StatusCode)
		}
	}

	resp := h.WithPermissions(schema.ContactWrite).POST(contactsPath, body)
	if resp.StatusCode != http.StatusCreated {
		t.Errorf("authorized create status = %d, error = %q", resp.StatusCode, resp.ErrorCode())
	}

	resp = h.WithPermissions(schema.ContactDelete).DELETE(contactsPath + "/" + id)
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("authorized delete status = %d, want 204", resp.StatusCode)
	}
}

func TestPersonalDataAccess(t *testing.T) {
	h := modeltest.NewHarness(t)
	id := createContact(t, h, map[string]any{
		"type": "person", "given_name": "Ama", "date_of_birth": "2000-01-01", "gender": "woman",
	})
	reader := h.WithPermissions(schema.ContactRead)
	writer := h.WithPermissions(schema.ContactWrite)

	for _, resp := range []*modeltest.Response{
		reader.GET(contactsPath + "/" + id),
		reader.GET(contactsPath),
		reader.GET(searchPath),
		writer.POST(contactsPath+"/preview", map[string]any{"id": id, "type": "person", "given_name": "Ama"}),
		writer.PUT(contactsPath+"/"+id, map[string]any{"notes": "Public identity"}),
		writer.POST(contactsPath, map[string]any{"type": "person", "given_name": "Esi"}),
	} {
		if resp.StatusCode >= 400 {
			t.Fatalf("read surface: status = %d, error = %v", resp.StatusCode, resp.JSON("error"))
		}
		assertPersonalOmitted(t, resp)
	}
	for field, value := range map[string]any{"date_of_birth": "2001-02-03", "gender": "free text", "date_of_birth_clear": nil, "gender_clear": nil} {
		field = strings.TrimSuffix(field, "_clear")
		for _, resp := range []*modeltest.Response{
			writer.POST(contactsPath, map[string]any{"type": "person", "given_name": "Esi", field: value}),
			writer.PUT(contactsPath+"/"+id, map[string]any{field: value}),
		} {
			if resp.StatusCode != http.StatusForbidden {
				t.Errorf("restricted %s write: status = %d, error = %v", field, resp.StatusCode, resp.JSON("error"))
			}
		}
	}
	preview := writer.POST(contactsPath+"/preview", map[string]any{"type": "person", "given_name": "Esi", "date_of_birth": "2000-01-01", "gender": "woman"})
	if preview.StatusCode != http.StatusOK {
		t.Fatalf("preview: status = %d", preview.StatusCode)
	}
	assertPersonalOmitted(t, preview)

	personalReader := h.WithPermissions(schema.ContactRead, schema.PersonalRead)
	got := personalReader.GET(contactsPath + "/" + id)
	if got.JSON("gender") != "woman" || got.JSON("date_of_birth") == nil {
		t.Errorf("personal read = %v %v", got.JSON("date_of_birth"), got.JSON("gender"))
	}

	personalWriter := h.WithPermissions(schema.ContactWrite, schema.PersonalWrite)
	resp := personalWriter.PUT(contactsPath+"/"+id, map[string]any{"gender": "self-described"})
	if resp.StatusCode != http.StatusOK {
		t.Errorf("write-only personal response: %d", resp.StatusCode)
	}
	assertPersonalOmitted(t, resp)
	if got := personalReader.GET(contactsPath + "/" + id).JSON("gender"); got != "self-described" {
		t.Errorf("persisted gender = %v", got)
	}
}

func assertPersonalOmitted(t *testing.T, resp *modeltest.Response) {
	t.Helper()
	var body any
	resp.ParseJSON(&body)
	var check func(any)
	check = func(value any) {
		switch value := value.(type) {
		case map[string]any:
			for key, child := range value {
				if key == "date_of_birth" || key == "gender" {
					t.Errorf("response exposes %s: %v", key, body)
				}
				check(child)
			}
		case []any:
			for _, child := range value {
				check(child)
			}
		}
	}
	check(body)
}
