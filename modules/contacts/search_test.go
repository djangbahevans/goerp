package contacts_test

import (
	"fmt"
	"net/http"
	"slices"
	"testing"

	"github.com/djangbahevans/goerp/sdk/go/modeltest"
)

const searchPath = contactsPath + "/search"

func searchNames(t *testing.T, h *modeltest.Harness, opts ...modeltest.QueryOption) []string {
	t.Helper()
	resp := h.GET(searchPath, opts...)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("search: status = %d, error = %v %v", resp.StatusCode, resp.JSON("error.code"), resp.JSON("error.message"))
	}
	var names []string
	for _, item := range resp.JSONArray("items") {
		names = append(names, item["display_name"].(string))
	}
	return names
}

func requireNames(t *testing.T, got []string, want ...string) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Errorf("names = %q, want %q", got, want)
	}
}

func TestSearchRanksExactThenPrefixThenOtherMatches(t *testing.T) {
	h := modeltest.NewHarness(t)
	for _, name := range []string{"Big Acme", "Acme Traders", "Acme", "Acme Foods", "Unrelated Ltd"} {
		createContact(t, h, map[string]any{"name": name})
	}

	got := searchNames(t, h, modeltest.WithQuery("q", "acme"))

	requireNames(t, got, "Acme", "Acme Foods", "Acme Traders", "Big Acme")
}

func TestSearchTiesBreakOnIDWhenDisplayNamesMatch(t *testing.T) {
	h := modeltest.NewHarness(t)
	first := createContact(t, h, map[string]any{"name": "Twin Ltd"})
	second := createContact(t, h, map[string]any{"name": "Twin Ltd"})

	resp := h.GET(searchPath, modeltest.WithQuery("q", "twin ltd"))
	items := resp.JSONArray("items")
	if len(items) != 2 {
		t.Fatalf("items = %d, want 2", len(items))
	}
	want := []string{first, second}
	slices.Sort(want)
	if got := []any{items[0]["id"], items[1]["id"]}; got[0] != want[0] || got[1] != want[1] {
		t.Errorf("ids = %v, want %v", got, want)
	}
}

func TestSearchKeepsTheLowestIDsOfATieGroupThatStraddlesThePageBoundary(t *testing.T) {
	h := modeltest.NewHarness(t)
	ids := make([]string, 0, 4)
	for range 4 {
		ids = append(ids, createContact(t, h, map[string]any{"name": "Twin Ltd"}))
	}
	slices.Sort(ids)

	resp := h.GET(searchPath, modeltest.WithQuery("q", "twin"), modeltest.WithQuery("limit", "2"))
	items := resp.JSONArray("items")
	if len(items) != 2 || items[0]["id"] != ids[0] || items[1]["id"] != ids[1] {
		t.Errorf("items = %v, want the two lowest IDs %v", items, ids[:2])
	}
	if resp.JSON("has_more") != true {
		t.Errorf("has_more = %v, want true", resp.JSON("has_more"))
	}
}

func TestSearchReturnsTheDocumentedItemShape(t *testing.T) {
	h := modeltest.NewHarness(t)
	company := createContact(t, h, map[string]any{"name": "Acme Ltd"})
	person := createContact(t, h, map[string]any{
		"type": "person", "name": "Ama Mensah", "company_id": company,
		"phone": "0200000000", "email": "ama@example.com",
	})

	resp := h.GET(searchPath, modeltest.WithQuery("q", "ama"))
	items := resp.JSONArray("items")
	if len(items) != 1 {
		t.Fatalf("items = %d, want 1", len(items))
	}
	for field, want := range map[string]any{
		"id": person, "display_name": "Ama Mensah (Acme Ltd)", "type": "person",
		"phone": "0200000000", "email": "ama@example.com",
	} {
		if got := items[0][field]; got != want {
			t.Errorf("item[%q] = %v, want %v", field, got, want)
		}
	}
	if got := resp.JSON("has_more"); got != false {
		t.Errorf("has_more = %v, want false", got)
	}
}

func TestSearchMatchesTheContactNameNotTheCompanyOfAPerson(t *testing.T) {
	h := modeltest.NewHarness(t)
	company := createContact(t, h, map[string]any{"name": "Globex Ltd"})
	createContact(t, h, map[string]any{"type": "person", "name": "Esi Owusu", "company_id": company})

	requireNames(t, searchNames(t, h, modeltest.WithQuery("q", "globex")), "Globex Ltd")
	requireNames(t, searchNames(t, h, modeltest.WithQuery("q", "esi")), "Esi Owusu (Globex Ltd)")
}

func TestSearchExactMatchIgnoresCase(t *testing.T) {
	h := modeltest.NewHarness(t)
	createContact(t, h, map[string]any{"name": "Acme Traders"})
	createContact(t, h, map[string]any{"name": "ACME"})

	requireNames(t, searchNames(t, h, modeltest.WithQuery("q", "acme")), "ACME", "Acme Traders")
}

func TestSearchFiltersByCustomerAndSupplier(t *testing.T) {
	h := modeltest.NewHarness(t)
	createContact(t, h, map[string]any{"name": "Buyer Ltd", "is_customer": true})
	createContact(t, h, map[string]any{"name": "Seller Ltd", "is_supplier": true})
	createContact(t, h, map[string]any{"name": "Both Ltd", "is_customer": true, "is_supplier": true})

	requireNames(t, searchNames(t, h, modeltest.WithQuery("is_customer", "true")), "Both Ltd", "Buyer Ltd")
	requireNames(t, searchNames(t, h, modeltest.WithQuery("is_supplier", "true")), "Both Ltd", "Seller Ltd")
	requireNames(t, searchNames(t, h, modeltest.WithQuery("is_customer", "true"), modeltest.WithQuery("is_supplier", "true")), "Both Ltd")
	requireNames(t, searchNames(t, h, modeltest.WithQuery("is_customer", "false")), "Seller Ltd")
}

func TestSearchRejectsAnInvalidFilter(t *testing.T) {
	h := modeltest.NewHarness(t)
	if resp := h.GET(searchPath, modeltest.WithQuery("is_customer", "maybe")); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestSearchExcludesArchivedContacts(t *testing.T) {
	h := modeltest.NewHarness(t)
	createContact(t, h, map[string]any{"name": "Active Ltd"})
	archived := createContact(t, h, map[string]any{"name": "Archived Ltd"})
	if resp := h.DELETE(contactsPath + "/" + archived); resp.StatusCode >= 400 {
		t.Fatalf("archive: status = %d", resp.StatusCode)
	}

	requireNames(t, searchNames(t, h, modeltest.WithQuery("q", "ltd")), "Active Ltd")
	requireNames(t, searchNames(t, h), "Active Ltd")
}

func TestSearchEmptyQueryReturnsTheFirstPageInDisplayNameOrder(t *testing.T) {
	h := modeltest.NewHarness(t)
	for _, name := range []string{"Charlie", "Alpha", "Bravo"} {
		createContact(t, h, map[string]any{"name": name})
	}

	requireNames(t, searchNames(t, h), "Alpha", "Bravo", "Charlie")
}

func TestSearchLimitDefaultsClampsAndReportsHasMore(t *testing.T) {
	h := modeltest.NewHarness(t)
	for i := range 105 {
		createContact(t, h, map[string]any{"name": fmt.Sprintf("Contact %03d", i)})
	}

	for name, tc := range map[string]struct {
		limit    string
		wantLen  int
		wantMore bool
	}{
		"default":      {"", 20, true},
		"explicit":     {"5", 5, true},
		"capped":       {"500", 100, true},
		"invalid":      {"many", 20, true},
		"non-positive": {"0", 20, true},
		"at the cap":   {"100", 100, true},
	} {
		t.Run(name, func(t *testing.T) {
			resp := h.GET(searchPath, modeltest.WithQuery("limit", tc.limit))
			if got := len(resp.JSONArray("items")); got != tc.wantLen {
				t.Errorf("items = %d, want %d", got, tc.wantLen)
			}
			if got := resp.JSON("has_more"); got != tc.wantMore {
				t.Errorf("has_more = %v, want %v", got, tc.wantMore)
			}
		})
	}

	resp := h.GET(searchPath, modeltest.WithQuery("q", "Contact 10"), modeltest.WithQuery("limit", "5"))
	if got := len(resp.JSONArray("items")); got != 5 {
		t.Fatalf("items = %d, want 5", got)
	}
	if got := resp.JSON("has_more"); got != false {
		t.Errorf("has_more with exactly the limit left = %v, want false", got)
	}
}

func TestSearchTreatsQueryTextAsLiteral(t *testing.T) {
	h := modeltest.NewHarness(t)
	for _, name := range []string{"100% Cotton", "Plain Cotton", "a_b Ltd", "axb Ltd", `back\slash Ltd`, "O'Brien Ltd"} {
		createContact(t, h, map[string]any{"name": name})
	}

	requireNames(t, searchNames(t, h, modeltest.WithQuery("q", "%")), "100% Cotton")
	requireNames(t, searchNames(t, h, modeltest.WithQuery("q", "a_b")), "a_b Ltd")
	requireNames(t, searchNames(t, h, modeltest.WithQuery("q", `\`)), `back\slash Ltd`)
	requireNames(t, searchNames(t, h, modeltest.WithQuery("q", "O'Brien")), "O'Brien Ltd")
	requireNames(t, searchNames(t, h, modeltest.WithQuery("q", "'; DROP TABLE contacts; --")))
	requireNames(t, searchNames(t, h, modeltest.WithQuery("q", "plain")), "Plain Cotton")
}
