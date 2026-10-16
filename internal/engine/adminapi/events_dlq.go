package adminapi

import (
	"encoding/json/v2"
	"net/http"
	"strconv"

	"github.com/djangbahevans/goerp/internal/engine/eventdelivery"
	"github.com/djangbahevans/goerp/internal/engine/jobqueue"
)

func (h *eventsHandlers) listDLQ(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	limit, offset := 50, 0
	for _, param := range []struct {
		name   string
		target *int
		min    int
		max    int
	}{
		{name: "limit", target: &limit, min: 1, max: 1000},
		{name: "offset", target: &offset, min: 0, max: 1_000_000_000},
	} {
		if value := query.Get(param.name); value != "" {
			n, err := strconv.Atoi(value)
			if err != nil || n < param.min || n > param.max {
				writeError(w, http.StatusBadRequest, "invalid_request", param.name+" is outside the supported range")
				return
			}
			*param.target = n
		}
	}

	page, err := h.deps.DLQ.List(r.Context(), eventdelivery.DLQFilter{
		TenantID: query.Get("tenant"), SubscriberModule: query.Get("module"),
	}, limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "list dead letter queue failed")
		return
	}
	writeData(w, http.StatusOK, page)
}

func (h *eventsHandlers) retryDLQ(w http.ResponseWriter, r *http.Request) {
	id, err := jobqueue.DecodeJobID(r.PathValue("id"))
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid_request", "id must be a positive job ID")
		return
	}

	count, err := h.deps.DLQ.Retry(r.Context(), eventdelivery.DLQFilter{}, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "retry dead letter delivery failed")
		return
	}
	if count == 0 {
		writeError(w, http.StatusNotFound, "not_found", "dead letter delivery not found")
		return
	}
	writeData(w, http.StatusOK, struct {
		ID string `json:"id"`
	}{ID: jobqueue.EncodeJobID(id)})
}

func (h *eventsHandlers) retryAllDLQ(w http.ResponseWriter, r *http.Request) {
	var filter *eventdelivery.DLQFilter
	err := json.UnmarshalRead(r.Body, &filter, json.RejectUnknownMembers(true))
	if err != nil || filter == nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "body must be an object containing optional tenant_id, event_name and subscriber_module filters")
		return
	}

	count, err := h.deps.DLQ.Retry(r.Context(), *filter, 0)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "retry dead letter deliveries failed")
		return
	}
	writeData(w, http.StatusOK, struct {
		Retried int64 `json:"retried"`
	}{Retried: count})
}
