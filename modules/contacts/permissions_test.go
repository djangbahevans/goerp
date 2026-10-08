package contacts_test

import (
	"net/http"
	"testing"

	"github.com/djangbahevans/goerp/modules/contacts/schema"
	"github.com/djangbahevans/goerp/sdk/go/modeltest"
)

func TestContactOperationPermissions(t *testing.T) {
	h := modeltest.NewHarness(t)
	id := createContact(t, h, map[string]any{"name": "Permission Test"})
	body := map[string]any{"name": "Restricted Contact"}

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
