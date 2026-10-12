package engine

import (
	"database/sql"
	"encoding/json/v2"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/djangbahevans/goerp/internal/engine/auth/authcheck"
	"github.com/djangbahevans/goerp/internal/engine/httperr"
	"github.com/djangbahevans/goerp/internal/engine/route"
	tenantresolve "github.com/djangbahevans/goerp/internal/engine/tenant/resolve"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
)

type resourceAccessMode uint8

const (
	ownRowsOnly resourceAccessMode = iota
	adminGated
)

type resourceRequest struct {
	r      *http.Request
	auth   *authcheck.AuthContext
	tenant *tenantresolve.TenantContext
}

type resourceError struct {
	status  int
	code    string
	message string
}

func invalidResourceRequest(message string) *resourceError {
	return &resourceError{http.StatusBadRequest, "invalid_request", message}
}

func resourceDenied(message string) *resourceError {
	return &resourceError{http.StatusForbidden, "permission_denied", message}
}

type resourceFilter struct {
	column string
	value  string
}

type engineResource[T any] struct {
	table              string
	accessMode         resourceAccessMode
	ownerColumn        string
	requiredPermission string
	name               string
	orderBy            string
	listWhere          string
	filters            func(resourceRequest) ([]resourceFilter, *resourceError)
	create             func(resourceRequest) (T, int, *resourceError)
	update             func(resourceRequest, string) (T, int, *resourceError)
	beforeDelete       func(resourceRequest, T) *resourceError
	deleteRow          func(resourceRequest, string) *resourceError
	decorate           func(resourceRequest, *T)
}

func (res engineResource[T]) withCreate[B any](fn func(resourceRequest, B) (T, int, *resourceError)) engineResource[T] {
	res.create = func(req resourceRequest) (T, int, *resourceError) {
		var body B
		if err := json.UnmarshalRead(req.r.Body, &body); err != nil {
			var zero T
			return zero, 0, &resourceError{http.StatusBadRequest, "invalid_body", "request body must be a JSON object"}
		}
		return fn(req, body)
	}
	return res
}

func (res engineResource[T]) withUpdate[B any](fn func(resourceRequest, string, B) (T, int, *resourceError)) engineResource[T] {
	res.update = func(req resourceRequest, id string) (T, int, *resourceError) {
		var body B
		if err := json.UnmarshalRead(req.r.Body, &body); err != nil {
			var zero T
			return zero, 0, &resourceError{http.StatusBadRequest, "invalid_body", "request body must be a JSON object"}
		}
		return fn(req, id, body)
	}
	return res
}

func (res engineResource[T]) serve(e *Engine, w http.ResponseWriter, r *http.Request) {
	req := resourceRequest{
		r:      r,
		auth:   authFromContext(r.Context()),
		tenant: tenantFromContext(r.Context()),
	}
	if req.auth == nil || req.tenant == nil {
		httperr.Write(r.Context(), w, http.StatusServiceUnavailable, "not_ready", "tenant/auth context not resolved")
		return
	}

	if res.accessMode == adminGated {
		if e.moduleRegistry == nil {
			httperr.Write(r.Context(), w, http.StatusServiceUnavailable, "not_ready", "engine has not finished starting")
			return
		}
		snap := e.moduleRegistry.Snapshot()
		if snap == nil {
			httperr.Write(r.Context(), w, http.StatusServiceUnavailable, "not_ready", "engine has not finished starting")
			return
		}
		if !hasPermission(snap.PermissionRegistry(), req.auth, res.requiredPermission) {
			httperr.Write(r.Context(), w, http.StatusForbidden, "permission_denied", "required permission: "+res.requiredPermission)
			return
		}
	}

	var failure *resourceError
	switch r.Method {
	case http.MethodGet:
		var data []T
		data, failure = res.list(e.primaryDB, req)
		if failure == nil {
			writeJSON(r.Context(), w, http.StatusOK, map[string]any{"data": data})
		}
	case http.MethodPost:
		data, status, err := res.create(req)
		failure = err
		if failure == nil {
			writeJSON(r.Context(), w, status, data)
		}
	case http.MethodPatch, http.MethodDelete:
		id := route.ParamsFromContext(r.Context())["id"]
		if id == "" {
			failure = &resourceError{http.StatusBadRequest, "invalid_path_param", "id path parameter is required"}
			break
		}

		row, err := res.resolve(e.primaryDB, req, id)
		if err != nil {
			failure = err
			break
		}

		if r.Method == http.MethodPatch {
			if res.update == nil {
				failure = &resourceError{http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed"}
				break
			}
			data, status, err := res.update(req, id)
			failure = err
			if failure == nil {
				writeJSON(r.Context(), w, status, data)
			}
			break
		}
		if res.beforeDelete != nil {
			if failure = res.beforeDelete(req, row); failure != nil {
				break
			}
		}

		failure = res.delete(e.primaryDB, req, id)
		if failure == nil {
			w.WriteHeader(http.StatusNoContent)
		}
	default:
		failure = &resourceError{http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed"}
	}

	if failure != nil {
		httperr.Write(r.Context(), w, failure.status, failure.code, failure.message)
	}
}

func (res engineResource[T]) list(pool *sql.DB, req resourceRequest) ([]T, *resourceError) {
	filters, failure := res.filters(req)
	if failure != nil {
		return nil, failure
	}
	if res.accessMode == ownRowsOnly {
		filters = append(filters, resourceFilter{res.ownerColumn, req.auth.UserID})
	}

	conditions := []string{"TRUE"}
	args := make([]any, 0, len(filters))
	for _, filter := range filters {
		args = append(args, filter.value)
		conditions = append(conditions, fmt.Sprintf("%s = $%d", filter.column, len(args)))
	}
	if res.listWhere != "" {
		conditions = append(conditions, res.listWhere)
	}

	query := fmt.Sprintf("SELECT to_jsonb(row) FROM %s.%s row WHERE %s ORDER BY %s", tenantschema.Name(req.tenant.Slug), res.table, strings.Join(conditions, " AND "), res.orderBy)
	rows, err := pool.QueryContext(req.r.Context(), query, args...)
	if err != nil {
		return nil, res.internalFailure("list")
	}
	defer rows.Close()

	out := []T{}
	for rows.Next() {
		var raw []byte
		var row T
		if err := rows.Scan(&raw); err != nil {
			return nil, res.internalFailure("list")
		}
		if err := json.Unmarshal(raw, &row); err != nil {
			return nil, res.internalFailure("list")
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, res.internalFailure("list")
	}

	if err := rows.Close(); err != nil {
		return nil, res.internalFailure("list")
	}
	if res.decorate != nil {
		for i := range out {
			res.decorate(req, &out[i])
		}
	}
	return out, nil
}

func (res engineResource[T]) resolve(pool *sql.DB, req resourceRequest, id string) (T, *resourceError) {
	var row T
	owner := "''"
	if res.accessMode == ownRowsOnly {
		owner = res.ownerColumn + "::text"
	}

	query := fmt.Sprintf("SELECT to_jsonb(row), %s FROM %s.%s row WHERE id = $1", owner, tenantschema.Name(req.tenant.Slug), res.table)
	var raw []byte
	var ownerID string
	err := pool.QueryRowContext(req.r.Context(), query, id).Scan(&raw, &ownerID)
	if errors.Is(err, sql.ErrNoRows) {
		return row, res.notFound()
	}
	if err != nil {
		return row, res.internalFailure("resolve")
	}
	if res.accessMode == ownRowsOnly && ownerID != req.auth.UserID {
		return row, resourceDenied("you do not own this " + res.name)
	}
	if err := json.Unmarshal(raw, &row); err != nil {
		return row, res.internalFailure("resolve")
	}
	return row, nil
}

func (res engineResource[T]) delete(pool *sql.DB, req resourceRequest, id string) *resourceError {
	if res.deleteRow != nil {
		return res.deleteRow(req, id)
	}

	query := fmt.Sprintf("DELETE FROM %s.%s WHERE id = $1", tenantschema.Name(req.tenant.Slug), res.table)
	args := []any{id}
	if res.accessMode == ownRowsOnly {
		query += " AND " + res.ownerColumn + " = $2"
		args = append(args, req.auth.UserID)
	}

	result, err := pool.ExecContext(req.r.Context(), query, args...)
	if err != nil {
		return res.internalFailure("delete")
	}

	count, err := result.RowsAffected()
	if err != nil {
		return res.internalFailure("delete")
	}
	if count == 0 {
		return res.notFound()
	}
	return nil
}

func (res engineResource[T]) internalFailure(operation string) *resourceError {
	return &resourceError{http.StatusInternalServerError, "internal_error", operation + " " + res.name + " failed"}
}

func (res engineResource[T]) notFound() *resourceError {
	return &resourceError{http.StatusNotFound, "not_found", res.name + " not found"}
}

func viewResourceFilters(req resourceRequest) ([]resourceFilter, *resourceError) {
	viewName := req.r.URL.Query().Get("view_name")
	if viewName == "" {
		return nil, invalidResourceRequest("view_name query parameter is required")
	}
	return []resourceFilter{{"view_name", viewName}}, nil
}
