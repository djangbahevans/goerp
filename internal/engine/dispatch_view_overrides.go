package engine

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"net/http"
	"slices"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/permission"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
)

type viewOverrideCreateRequest struct {
	ViewName string         `json:"view_name"`
	Element  string         `json:"element"`
	Property string         `json:"property"`
	Value    jsontext.Value `json:"value"`
}

type viewOverrideResponse struct {
	ID        string         `json:"id"`
	ViewName  string         `json:"view_name"`
	Element   string         `json:"element"`
	Property  string         `json:"property"`
	Value     jsontext.Value `json:"value"`
	UpdatedBy string         `json:"updated_by"`
	UpdatedAt time.Time      `json:"updated_at"`
}

func (e *Engine) viewOverridesResource() engineResource[viewOverrideResponse] {
	res := engineResource[viewOverrideResponse]{
		table:              "view_overrides",
		accessMode:         adminGated,
		requiredPermission: permission.ManageViewOverrides,
		name:               "view override",
		orderBy:            "element, property",
		filters:            viewResourceFilters,
	}

	return res.withCreate(func(req resourceRequest, body viewOverrideCreateRequest) (viewOverrideResponse, int, *resourceError) {
		if body.ViewName == "" || body.Element == "" || body.Property == "" || len(body.Value) == 0 {
			return viewOverrideResponse{}, 0, invalidResourceRequest("view_name, element, property, and value are required")
		}
		if !slices.Contains([]string{"label", "hidden", "required", "order", "help_text", "placeholder"}, body.Property) {
			return viewOverrideResponse{}, 0, invalidResourceRequest("unsupported override property")
		}

		query := fmt.Sprintf(`
            INSERT INTO %s.view_overrides (view_name, element, property, value, updated_by)
            VALUES ($1, $2, $3, $4, $5)
            ON CONFLICT (view_name, element, property) DO UPDATE
            SET value = EXCLUDED.value, updated_by = EXCLUDED.updated_by, updated_at = NOW()
            RETURNING to_jsonb(view_overrides), (xmax = 0)
        `, tenantschema.Name(req.tenant.Slug))
		var raw []byte
		var created bool
		err := e.primaryDB.QueryRowContext(req.r.Context(), query, body.ViewName, body.Element, body.Property, string(body.Value), req.auth.UserID).Scan(&raw, &created)
		if err != nil {
			return viewOverrideResponse{}, 0, res.internalFailure("create")
		}

		var out viewOverrideResponse
		if err := json.Unmarshal(raw, &out); err != nil {
			return viewOverrideResponse{}, 0, res.internalFailure("create")
		}

		status := http.StatusOK
		if created {
			status = http.StatusCreated
		}
		return out, status, nil
	})
}

func (e *Engine) dispatchViewOverridesRoute(w http.ResponseWriter, r *http.Request) {
	e.viewOverridesResource().serve(e, w, r)
}
