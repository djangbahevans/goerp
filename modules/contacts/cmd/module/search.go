package main

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/djangbahevans/goerp/modules/contacts/models"
	"github.com/djangbahevans/goerp/modules/contacts/schema"
	"github.com/djangbahevans/goerp/sdk/go/db"
	"github.com/djangbahevans/goerp/sdk/go/engine"
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
	ID          string  `db:"id" json:"id"`
	DisplayName string  `db:"display_name" json:"display_name"`
	Type        string  `db:"type" json:"type"`
	Phone       *string `db:"phone" json:"phone"`
	Email       *string `db:"email" json:"email"`
}

type searchResponse struct {
	Items   []searchItem `json:"items"`
	HasMore bool         `json:"has_more"`
}

func init() {
	engine.HandleAction(searchAction, searchContacts)
}

func searchContacts(req *engine.Request, _ engine.NoBody) *engine.Response {
	query := req.QueryParam("q")
	limit := searchLimit(req)

	var params []any
	param := func(value any) string {
		params = append(params, value)
		return "$" + strconv.Itoa(len(params))
	}

	conditions := []string{"deleted_at IS NULL"}
	for _, flag := range []string{"is_customer", "is_supplier"} {
		raw := req.QueryParam(flag)
		if raw == "" {
			continue
		}
		value, err := strconv.ParseBool(raw)
		if err != nil {
			return engine.BadRequest(fmt.Errorf("%s must be true or false", flag))
		}
		conditions = append(conditions, flag+" = "+param(value))
	}

	orderBy := "display_name, id"
	if query != "" {
		escaped := escapeLike(query)
		contains := param("%" + escaped + "%")
		conditions = append(conditions, "name ILIKE "+contains)
		orderBy = "CASE WHEN lower(name) = lower(" + param(query) + ") THEN 0 WHEN name ILIKE " +
			param(escaped+"%") + " THEN 1 ELSE 2 END, display_name, id"
	}

	sql := "SELECT id, display_name, type, phone, email FROM contacts WHERE " + strings.Join(conditions, " AND ") +
		" ORDER BY " + orderBy + " LIMIT " + param(limit+1)
	items, err := db.Query[searchItem](sql, params)
	if err != nil {
		return &engine.Response{StatusCode: 500, Body: map[string]any{
			"error": map[string]any{"code": "contacts.search_failed", "message": "search contacts: " + err.Error()},
		}}
	}

	hasMore := len(items) > limit
	if hasMore {
		items = items[:limit]
	}
	return engine.OK(searchResponse{Items: items, HasMore: hasMore})
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
