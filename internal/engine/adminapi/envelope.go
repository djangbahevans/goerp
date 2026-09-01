package adminapi

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"net/http"

	"github.com/rs/zerolog/log"
)

// envelope is the minimal version of engine-internals.md §11's
// "byte-identical response envelope" contract for the admin API:
// {"data": ..., "error": null} on success, {"data": null, "error": {...}}
// on failure. This is deliberately just the shape — request-size caps,
// concurrency limits, and admin_audit_log writes wrap around these
// handlers as separate middleware, not inside this helper.
type envelope struct {
	Data  any            `json:"data"`
	Error *envelopeError `json:"error"`
}

type envelopeError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// encodeEnvelope writes v with encoding/json v1's Encoder defaults, which
// MarshalWrite doesn't apply on its own: '<', '>', '&' escaped for safe
// HTML embedding, and U+2028/U+2029 escaped for safe JS embedding.
func encodeEnvelope(w http.ResponseWriter, v envelope) error {
	return json.MarshalWrite(w, v, jsontext.EscapeForHTML(true), jsontext.EscapeForJS(true))
}

func writeData(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := encodeEnvelope(w, envelope{Data: data}); err != nil {
		log.Error().Err(err).Msg("admin api: encode response")
	}
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := encodeEnvelope(w, envelope{Error: &envelopeError{Code: code, Message: message}}); err != nil {
		log.Error().Err(err).Msg("admin api: encode error response")
	}
}

// writeNotImplemented is the interface-seam response: the route exists and
// returns the documented envelope shape, but the subsystem behind it
// (a workflow, River, the invite system, ...) isn't wired up yet. issue
// names the goerp tracking issue a caller can check for progress.
func writeNotImplemented(w http.ResponseWriter, issue string) {
	writeError(w, http.StatusNotImplemented, "not_implemented", "not yet implemented, tracked as "+issue)
}
