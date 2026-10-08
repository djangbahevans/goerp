package main

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/djangbahevans/goerp/modules/contacts/models"
	"github.com/djangbahevans/goerp/modules/contacts/schema"
	"github.com/djangbahevans/goerp/sdk/go/engine"
	"github.com/djangbahevans/goerp/sdk/go/orm"
)

const (
	searchDefaultLimit = 20
	searchMaxLimit     = 100
)

var searchAction = engine.DefineAction[models.Contact, engine.NoBody]("search",
	engine.Scope(engine.CollectionAction),
	engine.Method(engine.MethodGet),
	engine.Requires(schema.ContactRead),
)

type searchItem struct {
	ID          string  `json:"id"`
	DisplayName string  `json:"display_name"`
	Type        string  `json:"type"`
	Phone       *string `json:"phone"`
	Email       *string `json:"email"`
}

type searchResponse struct {
	Items   []searchItem `json:"items"`
	HasMore bool         `json:"has_more"`
}

func init() {
	engine.HandleAction(searchAction, searchContacts)
}

// searchContacts reads through the ORM, so field-level access and record
// rules apply as they do for the generated List action. The ORM orders by one
// field, so the match tiers (exact, prefix, other) are separate queries and
// the ID tie-break is applied in Go.
func searchContacts(req *engine.Request, _ engine.NoBody) *engine.Response {
	query := req.QueryParam("q")
	limit := searchLimit(req)

	filter := orm.MatchAll[models.Contact]()
	for _, f := range []struct {
		flag  string
		field orm.Field[models.Contact, bool]
	}{
		{"is_customer", models.ContactFields.IsCustomer},
		{"is_supplier", models.ContactFields.IsSupplier},
	} {
		raw := req.QueryParam(f.flag)
		if raw == "" {
			continue
		}
		value, err := strconv.ParseBool(raw)
		if err != nil {
			return engine.BadRequest(fmt.Errorf("%s must be true or false", f.flag))
		}
		filter = filter.And(f.field.Eq(value))
	}

	tiers := []orm.Condition[models.Contact]{filter}
	if query != "" {
		name := models.ContactFields.Name
		escaped := escapeLike(query)
		exact := name.ILike(escaped)
		prefix := name.ILike(escaped + "%")
		contains := name.ILike("%" + escaped + "%")
		tiers = []orm.Condition[models.Contact]{
			filter.And(exact),
			filter.And(prefix).And(exact.Not()),
			filter.And(contains).And(prefix.Not()),
		}
	}

	items := []searchItem{}
	hasMore := false
	for _, tier := range tiers {
		want := limit - len(items)
		rows, err := searchTier(tier, want)
		if err != nil {
			return &engine.Response{StatusCode: 500, Body: map[string]any{
				"error": map[string]any{"code": "contacts.search_failed", "message": "search contacts: " + err.Error()},
			}}
		}
		if len(rows) > want {
			hasMore = true
			rows = rows[:want]
		}
		items = append(items, rows...)
		if hasMore {
			break
		}
	}
	return engine.OK(searchResponse{Items: items, HasMore: hasMore})
}

// searchTier returns up to want+1 matches of cond ordered by display name,
// then ID. The database orders by display name (its collation decides), and
// only runs of equal names are ordered by ID here. A tie group that straddles
// the cut is read by ID, since only its lowest IDs can make the page.
func searchTier(cond orm.Condition[models.Contact], want int) ([]searchItem, error) {
	fields := []orm.AnyField[models.Contact]{
		models.ContactFields.ID, models.ContactFields.DisplayName, models.ContactFields.Type,
		models.ContactFields.Phone, models.ContactFields.Email,
	}
	contacts, _, err := orm.From[models.Contact]().Where(cond).Select(fields...).
		OrderBy(models.ContactFields.DisplayName, false).Limit(want + 1).All()
	if err != nil {
		return nil, err
	}
	if want > 0 && len(contacts) > want && contacts[want].DisplayName != nil &&
		displayName(contacts[want]) == displayName(contacts[want-1]) {
		cut := displayName(contacts[want])
		group, _, err := orm.From[models.Contact]().Where(cond.And(models.ContactFields.DisplayName.Eq(cut))).
			Select(fields...).OrderBy(models.ContactFields.ID, false).Limit(want + 1).All()
		if err != nil {
			return nil, err
		}
		contacts = append(slices.DeleteFunc(contacts, func(c models.Contact) bool { return displayName(c) == cut }), group...)
	}
	for start := 0; start < len(contacts); {
		end := start + 1
		for end < len(contacts) && displayName(contacts[end]) == displayName(contacts[start]) {
			end++
		}
		slices.SortFunc(contacts[start:end], func(a, b models.Contact) int { return strings.Compare(a.ID, b.ID) })
		start = end
	}
	items := make([]searchItem, 0, min(len(contacts), want+1))
	for _, c := range contacts[:min(len(contacts), want+1)] {
		items = append(items, searchItem{
			ID: c.ID, DisplayName: displayName(c), Type: string(c.Type), Phone: c.Phone, Email: c.Email,
		})
	}
	return items, nil
}

func displayName(c models.Contact) string {
	if c.DisplayName == nil {
		return ""
	}
	return *c.DisplayName
}

// searchLimit clamps like the generated List action: an absent or invalid
// value is the default and an oversized one is the maximum.
func searchLimit(req *engine.Request) int {
	n := req.QueryParamInt("limit", searchDefaultLimit)
	if n < 1 {
		return searchDefaultLimit
	}
	return min(n, searchMaxLimit)
}

// escapeLike makes s match literally inside an ILIKE pattern.
func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}
