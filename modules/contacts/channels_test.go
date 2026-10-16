package contacts_test

import (
	"cmp"
	"database/sql"
	"maps"
	"net/http"
	"os"
	"slices"
	"sync"
	"testing"

	"github.com/djangbahevans/goerp/modules/contacts/schema"
	"github.com/djangbahevans/goerp/sdk/go/modeltest"
)

const channelsPath = "/contacts/channels"

func createChannel(t *testing.T, h *modeltest.Harness, contactID, kind, value string, extra map[string]any) string {
	t.Helper()
	body := map[string]any{"contact_id": contactID, "kind": kind, "value": value}
	maps.Copy(body, extra)
	response := h.POST(channelsPath, body)
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("create channel: status=%d error=%v", response.StatusCode, response.JSON("error"))
	}
	return response.JSON("id").(string)
}

func assertPrimary(t *testing.T, h *modeltest.Harness, contactID, kind, channelID string, value any) {
	t.Helper()
	contact := h.GET(contactsPath + "/" + contactID)
	if contact.StatusCode != 200 || contact.JSON(kind) != value {
		t.Fatalf("contact %s: status=%d %s=%v, want %v; error=%v", contactID, contact.StatusCode, kind, contact.JSON(kind), value, contact.JSON("error"))
	}
	var primary struct{ ID string }
	h.DB.QueryOne(&primary, `SELECT coalesce(min(id::text), '') AS id FROM contact_channels WHERE contact_id=$1 AND kind=$2 AND is_primary AND deleted_at IS NULL`, contactID, kind)
	if primary.ID != channelID {
		t.Errorf("primary=%s, want %s", primary.ID, channelID)
	}
}

func writeChannel(t *testing.T, h *modeltest.Harness, id string, body map[string]any) *modeltest.Response {
	t.Helper()
	response := h.PUT(channelsPath+"/"+id, body)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("update channel: status=%d error=%v", response.StatusCode, response.JSON("error"))
	}
	return response
}

func TestChannelPrimariesAndStoredContactValues(t *testing.T) {
	h := modeltest.NewHarness(t)
	person := createContact(t, h, map[string]any{"type": "person", "given_name": "Ama"})
	first := createChannel(t, h, person, "phone", "024 412 3456", nil)
	second := createChannel(t, h, person, "phone", "+233 24 555 1234", nil)
	email := createChannel(t, h, person, "email", "Ama@Example.test", nil)
	assertPrimary(t, h, person, "phone", first, "024 412 3456")
	assertPrimary(t, h, person, "email", email, "Ama@Example.test")
	if response := h.GET(channelsPath + "/" + second); response.JSON("is_primary") != false || response.JSON("label") != "other" || response.JSON("marketing_consent") != "unknown" || response.JSON("whatsapp") != false {
		t.Errorf("channel defaults: primary=%v label=%v consent=%v whatsapp=%v", response.JSON("is_primary"), response.JSON("label"), response.JSON("marketing_consent"), response.JSON("whatsapp"))
	}
	writeChannel(t, h, second, map[string]any{"is_primary": true})
	assertPrimary(t, h, person, "phone", second, "+233 24 555 1234")
	writeChannel(t, h, second, map[string]any{"value": "020 111 2222"})
	assertPrimary(t, h, person, "phone", second, "020 111 2222")
	event := h.Events.Last(schema.ContactUpdated).Payload
	if event.Phone == nil || *event.Phone != "020 111 2222" || !slices.Contains(event.ChangedFields, "phone") {
		t.Errorf("contact update event=%+v", event)
	}
	for _, id := range []string{second, first} {
		if response := h.DELETE(channelsPath + "/" + id); response.StatusCode != http.StatusNoContent {
			t.Fatalf("archive channel: status=%d error=%v", response.StatusCode, response.JSON("error"))
		}
		if id == second {
			assertPrimary(t, h, person, "phone", first, "024 412 3456")
		} else {
			assertPrimary(t, h, person, "phone", "", nil)
		}
	}
	assertPrimary(t, h, person, "email", email, "Ama@Example.test")
	h.DB.AssertIndexExists("idx_contact_channels_primary")
	h.DB.AssertIndexExists("idx_contact_channels_value")
	h.DB.AssertIndexExists("idx_contacts_phone_digits")
}

func TestChannelMovesAndExplicitDemotion(t *testing.T) {
	h := modeltest.NewHarness(t)
	firstContact := createContact(t, h, map[string]any{"type": "organization", "legal_name": "First"})
	otherContact := createContact(t, h, map[string]any{"type": "organization", "legal_name": "Other"})
	first := createChannel(t, h, firstContact, "phone", "024 000 0001", nil)
	second := createChannel(t, h, firstContact, "phone", "024 000 0002", nil)
	third := createChannel(t, h, firstContact, "phone", "024 000 0003", nil)
	writeChannel(t, h, first, map[string]any{"is_primary": false})
	assertPrimary(t, h, firstContact, "phone", first, "024 000 0001")
	writeChannel(t, h, first, map[string]any{"contact_id": otherContact})
	assertPrimary(t, h, firstContact, "phone", second, "024 000 0002")
	assertPrimary(t, h, otherContact, "phone", first, "024 000 0001")
	writeChannel(t, h, second, map[string]any{"kind": "email", "value": "second@example.test"})
	assertPrimary(t, h, firstContact, "phone", third, "024 000 0003")
	assertPrimary(t, h, firstContact, "email", second, "second@example.test")
	writeChannel(t, h, first, map[string]any{"is_primary": false})
	assertPrimary(t, h, otherContact, "phone", first, "024 000 0001")
	writeChannel(t, h, first, map[string]any{"contact_id": firstContact})
	assertPrimary(t, h, otherContact, "phone", "", nil)
	assertPrimary(t, h, firstContact, "phone", first, "024 000 0001")
}

func TestChannelValidationRollsBackPrimaryChanges(t *testing.T) {
	h := modeltest.NewHarness(t)
	contact := createContact(t, h, map[string]any{"type": "person", "given_name": "Ama"})
	first := createChannel(t, h, contact, "email", "ama@example.test", nil)
	response := h.POST(channelsPath, map[string]any{"contact_id": contact, "kind": "email", "value": "other@example.test", "is_primary": true, "whatsapp": true})
	if response.StatusCode != 400 || response.JSON("error.details.field") != "whatsapp" {
		t.Fatalf("invalid WhatsApp: status=%d error=%v", response.StatusCode, response.JSON("error"))
	}
	assertPrimary(t, h, contact, "email", first, "ama@example.test")
	for _, body := range []map[string]any{
		{"value": "   "}, {"kind": "fax"}, {"marketing_consent": "yes"}, {"label": "office"},
	} {
		response := h.PUT(channelsPath+"/"+first, body)
		if response.StatusCode != 400 {
			t.Errorf("invalid %v: status=%d error=%v", body, response.StatusCode, response.JSON("error"))
		}
		assertPrimary(t, h, contact, "email", first, "ama@example.test")
	}
	response = h.WithHeader("If-Match", "stale").PUT(channelsPath+"/"+first, map[string]any{"value": "changed@example.test"})
	if response.StatusCode != 409 {
		t.Errorf("stale etag: status=%d error=%v", response.StatusCode, response.JSON("error"))
	}
	assertPrimary(t, h, contact, "email", first, "ama@example.test")
}

func TestChannelConsentAndSharedValues(t *testing.T) {
	h := modeltest.NewHarness(t)
	for _, name := range []string{"Ama", "Esi"} {
		contact := createContact(t, h, map[string]any{"type": "person", "given_name": name})
		phone := createChannel(t, h, contact, "phone", "024 412 3456", map[string]any{"whatsapp": true, "label": "mobile"})
		email := createChannel(t, h, contact, "email", "shared@example.test", nil)
		assertPrimary(t, h, contact, "phone", phone, "024 412 3456")
		assertPrimary(t, h, contact, "email", email, "shared@example.test")
		response := writeChannel(t, h, phone, map[string]any{"marketing_consent": "granted", "consent_updated_at": "2026-10-16T12:00:00Z", "consent_source": "Signed form"})
		if response.JSON("marketing_consent") != "granted" || response.JSON("consent_source") != "Signed form" || response.JSON("consent_updated_at") != "2026-10-16T12:00:00Z" {
			t.Errorf("consent: state=%v source=%v timestamp=%v", response.JSON("marketing_consent"), response.JSON("consent_source"), response.JSON("consent_updated_at"))
		}
		writeChannel(t, h, phone, map[string]any{"marketing_consent": "withdrawn", "consent_updated_at": "2026-10-16T13:00:00Z", "consent_source": "Phone call"})
		assertPrimary(t, h, contact, "phone", phone, "024 412 3456")
	}
}

func TestConcurrentFirstChannelsHaveOnePrimary(t *testing.T) {
	h := modeltest.NewHarness(t)
	contact := createContact(t, h, map[string]any{"type": "organization", "legal_name": "Acme"})
	var responses [2]*modeltest.Response
	var wg sync.WaitGroup
	for i := range 2 {
		wg.Go(func() {
			responses[i] = h.POST(channelsPath, map[string]any{"contact_id": contact, "kind": "phone", "value": "024 412 3456"})
		})
	}
	wg.Wait()
	for _, response := range responses {
		if response.StatusCode != 201 {
			t.Fatalf("concurrent create: status=%d error=%v", response.StatusCode, response.JSON("error"))
		}
	}
	h.DB.AssertCount("contact_channels", 1, "is_primary AND deleted_at IS NULL")
	h.DB.AssertCount("contact_channels", 2, "deleted_at IS NULL")
	if response := h.GET(contactsPath + "/" + contact); response.JSON("phone") != "024 412 3456" {
		t.Errorf("primary phone=%v", response.JSON("phone"))
	}
}

func setTenantCountry(t *testing.T, h *modeltest.Harness, country string) {
	t.Helper()
	conn, err := sql.Open("pgx", cmp.Or(os.Getenv("GOERP_TEST_DB_DSN"), "postgres://goerp:dev@localhost:15432/goerp"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(t.Context(), "UPDATE system.tenants SET country=$1 WHERE id=$2", country, h.TenantID); err != nil {
		t.Fatal(err)
	}
}

func TestSearchMatchesOnlyPrimaryChannels(t *testing.T) {
	h := modeltest.NewHarness(t)
	setTenantCountry(t, h, "GH")
	contact := createContact(t, h, map[string]any{"type": "person", "given_name": "Ama"})
	phone := createChannel(t, h, contact, "phone", "024 412 3456", nil)
	createChannel(t, h, contact, "phone", "020 000 0000", nil)
	createChannel(t, h, contact, "email", "Ama@Example.test", nil)
	createChannel(t, h, contact, "email", "secondary@example.test", nil)
	for _, query := range []string{"024 412 3456", "024\u00a0412\u00a03456", "0244123456", "+233244123456", "233 24 412 3456", "AMA@example.TEST"} {
		requireNames(t, searchNames(t, h, modeltest.WithQuery("q", query)), "Ama")
	}
	for _, query := range []string{"0200000000", "secondary@example.test", "1244123456", "02441234567"} {
		requireNames(t, searchNames(t, h, modeltest.WithQuery("q", query)))
	}
	writeChannel(t, h, phone, map[string]any{"value": "+233 24 412 3456"})
	requireNames(t, searchNames(t, h, modeltest.WithQuery("q", "0244123456")), "Ama")
	setTenantCountry(t, h, "NG")
	requireNames(t, searchNames(t, h, modeltest.WithQuery("q", "0244123456")))
	requireNames(t, searchNames(t, h, modeltest.WithQuery("q", "+233244123456")), "Ama")
}

func TestChannelPermissionsAndParentVisibility(t *testing.T) {
	h := modeltest.NewHarness(t)
	contact := createContact(t, h, map[string]any{"type": "person", "given_name": "Ama"})
	channel := createChannel(t, h, contact, "phone", "024 412 3456", nil)
	reader := h.WithPermissions(schema.ContactRead)
	if response := reader.GET(channelsPath + "/" + channel); response.StatusCode != 200 {
		t.Fatalf("read channel: status=%d", response.StatusCode)
	}
	for _, response := range []*modeltest.Response{
		reader.POST(channelsPath, map[string]any{"contact_id": contact, "kind": "email", "value": "ama@example.test"}),
		reader.PUT(channelsPath+"/"+channel, map[string]any{"value": "024 000 0000"}),
		reader.DELETE(channelsPath + "/" + channel),
	} {
		if response.StatusCode != 403 {
			t.Errorf("read-only channel mutation: status=%d", response.StatusCode)
		}
	}
	assertPrimary(t, h, contact, "phone", channel, "024 412 3456")
	if response := h.WithPermissions(schema.ContactRead, schema.ContactWrite).DELETE(channelsPath + "/" + channel); response.StatusCode != 204 {
		t.Fatalf("channel archive with contact write: status=%d error=%v", response.StatusCode, response.JSON("error"))
	}
	assertPrimary(t, h, contact, "phone", "", nil)

	other := modeltest.NewHarness(t)
	foreignContact := createContact(t, other, map[string]any{"type": "organization", "legal_name": "Other tenant"})
	if response := h.POST(channelsPath, map[string]any{"contact_id": foreignContact, "kind": "phone", "value": "024 000 0000"}); response.StatusCode != 404 {
		t.Errorf("foreign parent: status=%d error=%v", response.StatusCode, response.JSON("error"))
	}
	if response := h.DELETE(contactsPath + "/" + contact); response.StatusCode != 204 {
		t.Fatalf("archive contact: status=%d", response.StatusCode)
	}
	if response := h.POST(channelsPath, map[string]any{"contact_id": contact, "kind": "phone", "value": "024 000 0000"}); response.StatusCode != 404 {
		t.Errorf("archived parent: status=%d error=%v", response.StatusCode, response.JSON("error"))
	}
	h.DB.AssertCount("contact_channels", 0, "deleted_at IS NULL")
}
