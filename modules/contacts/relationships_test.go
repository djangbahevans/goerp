package contacts_test

import (
	"maps"
	"net/http"
	"strings"
	"sync"
	"testing"
	"uuid"

	"github.com/djangbahevans/goerp/modules/contacts/schema"
	"github.com/djangbahevans/goerp/sdk/go/modeltest"
)

const relationshipsPath = "/contacts/relationships"
const relationshipTypesPath = "/contacts/relationship-types"

func createRelationshipType(t *testing.T, h *modeltest.Harness, extra map[string]any) string {
	t.Helper()

	body := map[string]any{"code": "parent", "name": "parent of", "inverse_name": "child of", "from_type": "person", "to_type": "person"}
	maps.Copy(body, extra)
	response := h.POST(relationshipTypesPath, body)
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("create type: status=%d error=%v", response.StatusCode, response.JSON("error"))
	}
	return response.JSON("id").(string)
}

func createRelationship(t *testing.T, h *modeltest.Harness, from, to, typ string, extra map[string]any) string {
	t.Helper()

	body := map[string]any{"contact_id": from, "related_contact_id": to, "type_id": typ}
	maps.Copy(body, extra)
	response := h.POST(relationshipsPath, body)
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("create relationship: status=%d error=%v", response.StatusCode, response.JSON("error"))
	}
	return response.JSON("id").(string)
}

func relationshipPair(t *testing.T, h *modeltest.Harness, id string) string {
	t.Helper()

	var row struct{ ID string }
	h.DB.QueryOne(&row, "SELECT other.id FROM contact_relationships r JOIN contact_relationships other ON other.pair_id = r.pair_id AND other.id <> r.id WHERE r.id = $1", id)
	return row.ID
}

func TestRelationshipPairLifecycle(t *testing.T) {
	h := modeltest.NewHarness(t)
	parent := createContact(t, h, map[string]any{"type": "person", "given_name": "Ama"})
	child := createContact(t, h, map[string]any{"type": "person", "given_name": "Esi"})
	other := createContact(t, h, map[string]any{"type": "person", "given_name": "Kofi"})
	typ := createRelationshipType(t, h, nil)
	id := createRelationship(t, h, parent, child, typ, map[string]any{"start_date": "2020-01-01", "notes": "Family"})
	inverse := relationshipPair(t, h, id)
	forward, reverse := h.GET(relationshipsPath+"/"+id), h.GET(relationshipsPath+"/"+inverse)
	if forward.JSON("label") != "parent of" || reverse.JSON("label") != "child of" || forward.JSON("pair_id") != reverse.JSON("pair_id") || reverse.JSON("contact_id") != child || reverse.JSON("related_contact_id") != parent {
		t.Fatalf("pair: forward=%v reverse=%v", forward, reverse)
	}
	for contact, label := range map[string]string{parent: "parent of", child: "child of"} {
		response := h.GET(relationshipsPath, modeltest.WithQuery("filter[contact_id]", contact))
		rows := response.JSONArray("data")
		if response.StatusCode != 200 || len(rows) != 1 || rows[0]["label"] != label {
			t.Fatalf("directional list: status=%d rows=%v", response.StatusCode, rows)
		}
	}
	for _, direction := range []string{id, inverse} {
		response := h.PUT(relationshipsPath+"/"+direction, map[string]any{"notes": "Updated", "end_date": "2021-01-01"})
		if response.StatusCode != 200 {
			t.Fatalf("update: status=%d error=%v", response.StatusCode, response.JSON("error"))
		}
		for _, row := range []string{id, inverse} {
			if got := h.GET(relationshipsPath + "/" + row); got.JSON("notes") != "Updated" || got.JSON("end_date") != "2021-01-01" {
				t.Fatalf("paired update: %v", got)
			}
		}
	}
	response := h.PUT(relationshipsPath+"/"+inverse, map[string]any{"related_contact_id": other})
	if response.StatusCode != 200 || h.GET(relationshipsPath+"/"+id).JSON("contact_id") != other {
		t.Fatalf("inverse move: status=%d error=%v", response.StatusCode, response.JSON("error"))
	}
	if response := h.DELETE(relationshipsPath + "/" + inverse); response.StatusCode != 204 {
		t.Fatalf("archive: status=%d error=%v", response.StatusCode, response.JSON("error"))
	}
	h.DB.AssertCount("contact_relationships", 2, "deleted_at IS NOT NULL")
	for _, index := range []string{"idx_contact_relationships_pair", "idx_contact_relationships_contact", "idx_contact_relationships_open", "idx_contact_relationship_types_code"} {
		h.DB.AssertIndexExists(index)
	}
}

func TestRelationshipUniquenessRollbackAndServerFields(t *testing.T) {
	h := modeltest.NewHarness(t)
	a := createContact(t, h, map[string]any{"type": "person", "given_name": "A"})
	b := createContact(t, h, map[string]any{"type": "person", "given_name": "B"})
	typ := createRelationshipType(t, h, nil)
	id := createRelationship(t, h, a, b, typ, nil)
	for _, parties := range [][2]string{{a, b}, {b, a}} {
		response := h.POST(relationshipsPath, map[string]any{"contact_id": parties[0], "related_contact_id": parties[1], "type_id": typ})
		if response.StatusCode != 409 {
			t.Errorf("duplicate: status=%d error=%v", response.StatusCode, response.JSON("error"))
		}
	}
	for _, field := range []string{"pair_id", "direction", "label", "unknown", "created_at"} {
		body := map[string]any{"contact_id": a, "related_contact_id": b, "type_id": typ, "end_date": "2020-01-01", field: "override"}
		for _, response := range []*modeltest.Response{h.POST(relationshipsPath, body), h.PUT(relationshipsPath+"/"+id, body)} {
			if response.StatusCode != 400 {
				t.Errorf("server field %s: status=%d error=%v", field, response.StatusCode, response.JSON("error"))
			}
		}
	}
	if response := h.WithHeader("If-Match", "stale").PUT(relationshipsPath+"/"+relationshipPair(t, h, id), map[string]any{"notes": "Stale"}); response.StatusCode != 409 {
		t.Errorf("stale etag: status=%d", response.StatusCode)
	}
	h.DB.AssertCount("contact_relationships", 2, "end_date IS NULL AND notes IS NULL AND deleted_at IS NULL")
	assertRelationshipStatus(t, h.PUT(relationshipsPath+"/"+id, map[string]any{"end_date": "2020-01-01"}), 200)
	second := createRelationship(t, h, b, a, typ, nil)
	assertRelationshipStatus(t, h.PUT(relationshipsPath+"/"+id, map[string]any{"end_date": nil}), 409)
	assertRelationshipStatus(t, h.DELETE(relationshipsPath+"/"+second), 204)
	assertRelationshipStatus(t, h.PUT(relationshipsPath+"/"+id, map[string]any{"end_date": nil}), 200)
}

func TestRelationshipKindsSymmetryAndTypeRules(t *testing.T) {
	h := modeltest.NewHarness(t)
	a := createContact(t, h, map[string]any{"type": "organization", "legal_name": "Branch"})
	b := createContact(t, h, map[string]any{"type": "organization", "legal_name": "Head office"})
	person := createContact(t, h, map[string]any{"type": "person", "given_name": "Ama"})
	typ := createRelationshipType(t, h, map[string]any{"code": "branch", "name": "branch of", "inverse_name": "has branch", "from_type": "organization", "to_type": "organization"})
	id := createRelationship(t, h, a, b, typ, nil)
	for _, body := range []map[string]any{{"contact_id": person}, {"related_contact_id": person}, {"related_contact_id": a}} {
		assertRelationshipStatus(t, h.PUT(relationshipsPath+"/"+id, body), 400)
	}
	assertRelationshipStatus(t, h.PUT(contactsPath+"/"+a, map[string]any{"type": "person", "legal_name": nil, "given_name": "Changed"}), 400)
	assertRelationshipStatus(t, h.PUT(relationshipTypesPath+"/"+typ, map[string]any{"from_type": "person"}), 400)
	assertRelationshipStatus(t, h.PUT(relationshipTypesPath+"/"+typ, map[string]any{"name": "office of", "inverse_name": "has office"}), 200)
	if h.GET(relationshipsPath+"/"+id).JSON("label") != "office of" || h.GET(relationshipsPath+"/"+relationshipPair(t, h, id)).JSON("label") != "has office" {
		t.Fatal("type labels did not recompute")
	}
	symmetric := createRelationshipType(t, h, map[string]any{"code": "partner", "name": "partner of", "inverse_name": nil, "symmetric": true, "from_type": "organization", "to_type": "organization"})
	assertRelationshipStatus(t, h.PUT(relationshipsPath+"/"+id, map[string]any{"type_id": symmetric}), 200)
	if h.GET(relationshipsPath+"/"+id).JSON("label") != "partner of" || h.GET(relationshipsPath+"/"+relationshipPair(t, h, id)).JSON("label") != "partner of" {
		t.Fatal("symmetric labels differ")
	}
	for _, extra := range []map[string]any{{"code": "invalid1", "symmetric": true}, {"code": "invalid2", "inverse_name": nil}, {"code": "invalid3", "inverse_name": ""}, {"code": "invalid4", "symmetric": true, "inverse_name": nil, "to_type": "organization"}} {
		body := map[string]any{"name": "invalid", "from_type": "person", "to_type": "person", "inverse_name": "inverse"}
		maps.Copy(body, extra)
		assertRelationshipStatus(t, h.POST(relationshipTypesPath, body), 400)
	}
}

func TestRelationshipRetainsArchivedPartiesAndProtectsModuleTypes(t *testing.T) {
	h := modeltest.NewHarness(t)
	a := createContact(t, h, map[string]any{"type": "person", "given_name": "A"})
	b := createContact(t, h, map[string]any{"type": "person", "given_name": "B"})
	typ := uuid.New().String()
	h.DB.Seed("contact_relationship_types", map[string]any{"id": typ, "code": "parent", "name": "parent of", "inverse_name": "child of", "from_type": "person", "to_type": "person", "owner_module": "school"})
	id := createRelationship(t, h, a, b, typ, nil)
	assertRelationshipStatus(t, h.DELETE(contactsPath+"/"+b), 204)
	assertRelationshipStatus(t, h.PUT(relationshipsPath+"/"+id, map[string]any{"related_contact_id": strings.ToUpper(b), "notes": "Retained"}), 200)
	assertRelationshipStatus(t, h.POST(relationshipsPath, map[string]any{"contact_id": a, "related_contact_id": b, "type_id": typ, "end_date": "2020-01-01"}), 404)
	for _, body := range []map[string]any{{"code": "changed"}, {"from_type": "organization"}, {"symmetric": true, "inverse_name": nil}, {"owner_module": nil}} {
		assertRelationshipStatus(t, h.PUT(relationshipTypesPath+"/"+typ, body), 400)
	}
	assertRelationshipStatus(t, h.DELETE(relationshipTypesPath+"/"+typ), 400)
	assertRelationshipStatus(t, h.PUT(relationshipTypesPath+"/"+typ, map[string]any{"name": "guardian of", "is_active": false}), 200)
	assertRelationshipStatus(t, h.PUT(relationshipsPath+"/"+id, map[string]any{"notes": "Inactive type retained"}), 200)
	assertRelationshipStatus(t, h.DELETE(relationshipsPath+"/"+id), 204)
	h.DB.AssertCount("contact_relationships", 2, "deleted_at IS NOT NULL")
}

func TestRelationshipPermissionsTenantIsolationAndConcurrentCreates(t *testing.T) {
	h := modeltest.NewHarness(t)
	a := createContact(t, h, map[string]any{"type": "person", "given_name": "A"})
	b := createContact(t, h, map[string]any{"type": "person", "given_name": "B"})
	typ := createRelationshipType(t, h, nil)
	var responses [2]*modeltest.Response
	var wg sync.WaitGroup
	for i := range 2 {
		wg.Go(func() {
			from, to := a, b
			if i == 1 {
				from, to = b, a
			}
			responses[i] = h.POST(relationshipsPath, map[string]any{"contact_id": from, "related_contact_id": to, "type_id": typ})
		})
	}
	wg.Wait()
	var id string
	for _, response := range responses {
		switch response.StatusCode {
		case 201:
			id = response.JSON("id").(string)
		case 409:
		default:
			t.Fatalf("concurrent create: status=%d error=%v", response.StatusCode, response.JSON("error"))
		}
	}
	h.DB.AssertCount("contact_relationships", 2, "deleted_at IS NULL")
	reader := h.WithPermissions(schema.ContactRead)
	assertRelationshipStatus(t, reader.GET(relationshipsPath+"/"+id), 200)
	assertRelationshipStatus(t, reader.PUT(relationshipsPath+"/"+id, map[string]any{"notes": "Denied"}), 403)
	assertRelationshipStatus(t, reader.DELETE(relationshipsPath+"/"+id), 403)
	assertRelationshipStatus(t, reader.POST(relationshipTypesPath, map[string]any{}), 403)
	other := modeltest.NewHarness(t)
	foreign := createContact(t, other, map[string]any{"type": "person", "given_name": "Foreign"})
	assertRelationshipStatus(t, h.PUT(relationshipsPath+"/"+id, map[string]any{"related_contact_id": foreign}), 404)
	assertRelationshipStatus(t, h.PUT(relationshipsPath+"/"+id, map[string]any{"type_id": createRelationshipType(t, other, nil)}), 404)
}

func assertRelationshipStatus(t *testing.T, response *modeltest.Response, status int) {
	t.Helper()
	if response.StatusCode != status {
		t.Fatalf("status=%d want=%d error=%v", response.StatusCode, status, response.JSON("error"))
	}
}
