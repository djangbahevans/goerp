package engine

import (
	"database/sql"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"net/http"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/permission"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
)

type viewOverrideCreateRequest struct {
	ViewName string         `json:"view_name"`
	Element  string         `json:"element"`
	Property string         `json:"property"`
	Value    jsontext.Value `json:"value"`
	Required jsontext.Value `json:"required,omitempty"`
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

func overrideValue(property string, raw jsontext.Value) (any, error) {
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, fmt.Errorf("invalid override value: %w", err)
	}
	if decoded == nil {
		return nil, fmt.Errorf("override value must not be null")
	}
	switch property {
	case "label", "help_text", "placeholder":
		var value string
		err := json.Unmarshal(raw, &value)
		return value, err
	case "hidden", "required":
		var value bool
		err := json.Unmarshal(raw, &value)
		return value, err
	case "order":
		var value int
		if err := json.Unmarshal(raw, &value); err != nil || value < 0 {
			return nil, fmt.Errorf("order must be a nonnegative integer")
		}
		return value, nil
	default:
		return nil, fmt.Errorf("unsupported override property")
	}
}

func (e *Engine) viewOverridesResource() engineResource[viewOverrideResponse] {
	res := engineResource[viewOverrideResponse]{
		table:              "view_overrides",
		accessMode:         adminGated,
		requiredPermission: permission.ManageViewOverrides,
		name:               "view override",
		orderBy:            "element, property",
		filters:            viewResourceFilters,
		deleteRow:          e.deleteViewOverride,
	}

	return res.withCreate(func(req resourceRequest, body viewOverrideCreateRequest) (viewOverrideResponse, int, *resourceError) {
		if body.ViewName == "" || body.Element == "" || body.Property == "" || len(body.Value) == 0 {
			return viewOverrideResponse{}, 0, invalidResourceRequest("view_name, element, property, and value are required")
		}
		value, err := overrideValue(body.Property, body.Value)
		if err != nil {
			return viewOverrideResponse{}, 0, invalidResourceRequest(err.Error())
		}
		hasCompanion := len(body.Required) != 0
		if hasCompanion {
			required, err := overrideValue("required", body.Required)
			if err != nil || body.Property != "hidden" || value != true || required != false {
				return viewOverrideResponse{}, 0, invalidResourceRequest("required companion must be false and accompany hidden: true")
			}
		}

		tx, rows, failure := e.beginViewOverrideWrite(req)
		if failure != nil {
			return viewOverrideResponse{}, 0, failure
		}
		defer func() { _ = tx.Rollback() }()

		candidate := viewOverrideResponse{
			ViewName: body.ViewName,
			Element:  body.Element,
			Property: body.Property,
			Value:    body.Value,
		}
		rows = replaceViewOverride(rows, candidate)
		if hasCompanion {
			rows = replaceViewOverride(rows, viewOverrideResponse{ViewName: body.ViewName, Element: body.Element, Property: "required", Value: jsontext.Value("false")})
		}
		if failure := validateViewOverrideState(e.moduleRegistry.Snapshot().SchemaResponse(), rows, body.ViewName, body.Element); failure != nil {
			return viewOverrideResponse{}, 0, failure
		}

		out, created, err := upsertViewOverride(tx, req, candidate)
		if err != nil {
			return viewOverrideResponse{}, 0, res.internalFailure("create")
		}
		if hasCompanion {
			companion := viewOverrideResponse{ViewName: body.ViewName, Element: body.Element, Property: "required", Value: jsontext.Value("false")}
			if _, _, err := upsertViewOverride(tx, req, companion); err != nil {
				return viewOverrideResponse{}, 0, res.internalFailure("create")
			}
		}
		if err := tx.Commit(); err != nil {
			return viewOverrideResponse{}, 0, res.internalFailure("create")
		}

		status := http.StatusOK
		if created {
			status = http.StatusCreated
		}
		return out, status, nil
	})
}

func replaceViewOverride(rows []viewOverrideResponse, candidate viewOverrideResponse) []viewOverrideResponse {
	for i, row := range rows {
		if row.ViewName == candidate.ViewName && row.Element == candidate.Element && row.Property == candidate.Property {
			rows[i] = candidate
			return rows
		}
	}
	return append(rows, candidate)
}

func (e *Engine) beginViewOverrideWrite(req resourceRequest) (*sql.Tx, []viewOverrideResponse, *resourceError) {
	failure := &resourceError{http.StatusInternalServerError, "internal_error", "write view override failed"}
	tx, err := e.primaryDB.BeginTx(req.r.Context(), nil)
	if err != nil {
		return nil, nil, failure
	}

	// A tenant lock also serializes writes to properties whose rows do not exist yet.
	if _, err := tx.ExecContext(req.r.Context(), "SELECT pg_advisory_xact_lock(hashtextextended($1, 0))", tenantschema.Name(req.tenant.Slug)+".view_overrides"); err != nil {
		_ = tx.Rollback()
		return nil, nil, failure
	}
	rows, err := loadViewOverrides(req.r.Context(), tx, req.tenant.Slug)
	if err != nil {
		_ = tx.Rollback()
		return nil, nil, failure
	}
	return tx, rows, nil
}

func upsertViewOverride(tx *sql.Tx, req resourceRequest, row viewOverrideResponse) (viewOverrideResponse, bool, error) {
	query := fmt.Sprintf(`
        INSERT INTO %s.view_overrides (view_name, element, property, value, updated_by)
        VALUES ($1, $2, $3, $4, $5)
        ON CONFLICT (view_name, element, property) DO UPDATE
        SET value = EXCLUDED.value, updated_by = EXCLUDED.updated_by, updated_at = NOW()
        RETURNING to_jsonb(view_overrides), (xmax = 0)
    `, tenantschema.Name(req.tenant.Slug))
	var raw []byte
	var created bool
	if err := tx.QueryRowContext(req.r.Context(), query, row.ViewName, row.Element, row.Property, string(row.Value), req.auth.UserID).Scan(&raw, &created); err != nil {
		return viewOverrideResponse{}, false, err
	}
	var out viewOverrideResponse
	err := json.Unmarshal(raw, &out)
	return out, created, err
}

func (e *Engine) deleteViewOverride(req resourceRequest, id string) *resourceError {
	tx, rows, failure := e.beginViewOverrideWrite(req)
	if failure != nil {
		return failure
	}
	defer func() { _ = tx.Rollback() }()

	var deleted viewOverrideResponse
	remaining := make([]viewOverrideResponse, 0, len(rows))
	for _, row := range rows {
		if row.ID == id {
			deleted = row
		} else {
			remaining = append(remaining, row)
		}
	}
	if deleted.ID == "" {
		return e.viewOverridesResource().notFound()
	}
	if failure := validateViewOverrideState(e.moduleRegistry.Snapshot().SchemaResponse(), remaining, deleted.ViewName, deleted.Element); failure != nil {
		return failure
	}

	query := fmt.Sprintf("DELETE FROM %s.view_overrides WHERE id = $1", tenantschema.Name(req.tenant.Slug))
	if _, err := tx.ExecContext(req.r.Context(), query, id); err != nil {
		return e.viewOverridesResource().internalFailure("delete")
	}
	if err := tx.Commit(); err != nil {
		return e.viewOverridesResource().internalFailure("delete")
	}
	return nil
}

func (e *Engine) dispatchViewOverridesRoute(w http.ResponseWriter, r *http.Request) {
	e.viewOverridesResource().serve(e, w, r)
}
