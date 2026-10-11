package abi

import (
	"net/http"
	"strings"
)

// FrameworkError marks SDK-generated errors whose code and status must match
// FrameworkErrorStatus. Every other error requires a module declaration.
const FrameworkError = "framework"

// FrameworkErrorStatus returns the status of a supported SDK framework code,
// or zero for a code that requires a module declaration.
func FrameworkErrorStatus(code string) int {
	switch code {
	case "invalid_body", "engine.invalid_request":
		return http.StatusBadRequest
	case "engine.route_not_found":
		return http.StatusNotFound
	case "engine.marshal_failed":
		return http.StatusInternalServerError
	case "orm.validation_failed":
		return http.StatusUnprocessableEntity
	default:
		return 0
	}
}

// ApplicationErrorStatus maps standard module.resource.condition codes to HTTP
// statuses. A custom condition retains fallback when it is an error status;
// otherwise it returns 500.
func ApplicationErrorStatus(code string, fallback int) int {
	_, rest, ok := strings.Cut(code, ".")
	if !ok {
		return http.StatusInternalServerError
	}

	resource, condition, ok := strings.Cut(rest, ".")
	if !ok || strings.Contains(condition, ".") {
		return http.StatusInternalServerError
	}

	switch {
	case condition == "not_found":
		return http.StatusNotFound
	case condition == "forbidden":
		return http.StatusForbidden
	case condition == "self_reference", condition == "invalid_state_transition",
		strings.HasPrefix(condition, "duplicate_"), strings.HasPrefix(condition, "circular_"), strings.HasPrefix(condition, "already_"):
		return http.StatusConflict
	case condition == "limit_exceeded", strings.HasPrefix(condition, "invalid_"), strings.HasPrefix(condition, "missing_"),
		resource == "import" && condition == "row_validation_failed":
		return http.StatusUnprocessableEntity
	case condition == "unavailable":
		return http.StatusServiceUnavailable
	case fallback >= http.StatusBadRequest && fallback <= 599:
		return fallback
	default:
		return http.StatusInternalServerError
	}
}
