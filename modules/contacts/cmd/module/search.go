package main

import (
	"errors"
	"slices"
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
	ID           string              `json:"id"`
	DisplayName  string              `json:"display_name"`
	Type         string              `json:"type"`
	IsActive     bool                `json:"is_active"`
	Phone        *string             `json:"phone"`
	Email        *string             `json:"email"`
	Organization *searchOrganization `json:"organization"`
}

type searchOrganization struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
}

type searchResponse struct {
	Items   []searchItem `json:"items"`
	HasMore bool         `json:"has_more"`
}

func init() {
	engine.HandleAction(searchAction, searchContacts)
}

// The ORM supports one ordering field, so rank tiers use separate queries
// and equal sort names receive their ID tie-break in Go.
func searchContacts(req *engine.Request, _ engine.NoBody) *engine.Response {
	query := req.QueryParam("q")
	limit := searchLimit(req)

	filter := orm.MatchAll[models.Contact]()
	if kind := req.QueryParam("type"); kind != "" {
		if kind != "person" && kind != "organization" {
			return engine.BadRequest(errors.New("type must be person or organization"))
		}
		filter = filter.And(models.ContactFields.Type.Eq(models.ContactType(kind)))
	}

	tiers := []orm.Condition[models.Contact]{filter}
	if query != "" {
		name := models.ContactFields.FullName
		escaped := escapeLike(query)
		exact := name.ILike(escaped)
		prefix := name.ILike(escaped + "%")
		pattern := "%" + escaped + "%"
		contains := name.ILike(pattern).Or(models.ContactFields.PreferredName.ILike(pattern)).Or(models.ContactFields.TradingName.ILike(pattern))
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

// A tie group crossing the page boundary needs its lowest IDs, independently
// of the database's order within that group.
func searchTier(cond orm.Condition[models.Contact], want int) ([]searchItem, error) {
	fields := []orm.AnyField[models.Contact]{
		models.ContactFields.ID, models.ContactFields.DisplayName, models.ContactFields.Type,
		models.ContactFields.SortName, models.ContactFields.IsActive,
		models.ContactFields.Phone, models.ContactFields.Email,
	}
	contacts, _, err := orm.From[models.Contact]().Where(cond).Select(fields...).
		OrderBy(models.ContactFields.SortName, false).Limit(want + 1).All()
	if err != nil {
		return nil, err
	}
	if want > 0 && len(contacts) > want && contacts[want].SortName == contacts[want-1].SortName {
		cut := contacts[want].SortName
		group, _, err := orm.From[models.Contact]().Where(cond.And(models.ContactFields.SortName.Eq(cut))).
			Select(fields...).OrderBy(models.ContactFields.ID, false).Limit(want + 1).All()
		if err != nil {
			return nil, err
		}
		contacts = append(slices.DeleteFunc(contacts, func(c models.Contact) bool { return c.SortName == cut }), group...)
	}
	for start := 0; start < len(contacts); {
		end := start + 1
		for end < len(contacts) && contacts[end].SortName == contacts[start].SortName {
			end++
		}
		slices.SortFunc(contacts[start:end], func(a, b models.Contact) int { return strings.Compare(a.ID, b.ID) })
		start = end
	}
	items := make([]searchItem, 0, min(len(contacts), want+1))
	for _, c := range contacts[:min(len(contacts), want+1)] {
		items = append(items, searchItem{
			ID:          c.ID,
			DisplayName: c.DisplayName,
			Type:        string(c.Type),
			IsActive:    c.IsActive,
			Phone:       c.Phone,
			Email:       c.Email,
		})
	}
	return items, nil
}

func searchLimit(req *engine.Request) int {
	n := req.QueryParamInt("limit", searchDefaultLimit)
	if n < 1 {
		return searchDefaultLimit
	}
	return min(n, searchMaxLimit)
}

func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}
