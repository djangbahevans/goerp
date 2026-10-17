package adminapi

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/eventdelivery"
	"github.com/djangbahevans/goerp/internal/engine/jobqueue"
	"github.com/djangbahevans/goerp/internal/engine/registry"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
)

const replayConfirmPhrase = "REPLAY EVENTS"

const defaultReplayBatchSize = 100

type EventsDeps struct {
	ModuleRegistry *registry.ModuleRegistry
	TenantStore    *tenant.Store
	Pool           *sql.DB
	JobClient      *river.Client[pgx.Tx]
	DLQ            *eventdelivery.DLQ
}

func RegisterEventsRoutes(mux *http.ServeMux, deps EventsDeps) {
	h := &eventsHandlers{deps: deps}
	mux.HandleFunc("GET /admin/events", h.listEvents)
	mux.HandleFunc("POST /admin/events/replay", h.replay)
	mux.HandleFunc("GET /admin/events/dlq", h.listDLQ)
	mux.HandleFunc("POST /admin/events/dlq/{id}/retry", h.retryDLQ)
	mux.HandleFunc("POST /admin/events/dlq/retry-all", h.retryAllDLQ)
}

type eventsHandlers struct {
	deps EventsDeps
}

type replayRequest struct {
	Tenant     string   `json:"tenant"`
	Event      []string `json:"event"`
	Module     string   `json:"module"`
	Subscriber []string `json:"subscriber"`
	From       string   `json:"from"`
	To         string   `json:"to"`
	BatchSize  int      `json:"batch_size"`
	DryRun     bool     `json:"dry_run"`
	Confirm    string   `json:"confirm"`
}

type replayDryRunResponse struct {
	EventCount               int `json:"event_count"`
	JobCount                 int `json:"job_count"`
	EstimatedDurationMinutes int `json:"estimated_duration_minutes"`
}

func (h *eventsHandlers) replay(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSON[replayRequest](r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "invalid request body: "+err.Error())
		return
	}

	if req.Tenant == "" || len(req.Event) == 0 || req.From == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "tenant, event, and from are required")
		return
	}

	from, err := time.Parse(time.RFC3339, req.From)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "from must be RFC 3339: "+err.Error())
		return
	}
	to := time.Now()
	if req.To != "" {
		to, err = time.Parse(time.RFC3339, req.To)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", "to must be RFC 3339: "+err.Error())
			return
		}
	}
	batchSize := req.BatchSize
	if batchSize <= 0 {
		batchSize = defaultReplayBatchSize
	}

	filter := jobqueue.EventsReplayArgs{
		Tenant:      req.Tenant,
		EventNames:  req.Event,
		Module:      req.Module,
		Subscribers: req.Subscriber,
		From:        from,
		To:          to,
		BatchSize:   batchSize,
	}

	if req.DryRun {
		eventCount, jobCount, err := eventdelivery.CountReplayMatches(r.Context(), h.deps.Pool, h.deps.ModuleRegistry, h.deps.TenantStore, filter)
		if err != nil {
			if errors.Is(err, tenant.ErrTenantNotFound) {
				writeError(w, http.StatusNotFound, "not_found", "tenant not found")
				return
			}
			writeError(w, http.StatusInternalServerError, "internal", err.Error())
			return
		}
		writeData(w, http.StatusOK, replayDryRunResponse{
			EventCount:               eventCount,
			JobCount:                 jobCount,
			EstimatedDurationMinutes: eventdelivery.EstimatedDurationMinutes(jobCount, batchSize),
		})
		return
	}

	if len(req.Subscriber) == 0 || req.Tenant == "all" {
		if req.Confirm != replayConfirmPhrase {
			writeError(w, http.StatusBadRequest, "invalid_request", fmt.Sprintf("confirm must equal %q", replayConfirmPhrase))
			return
		}
	}

	result, err := h.deps.JobClient.Insert(r.Context(), filter, nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}

	jobID := jobqueue.EncodeJobID(result.Job.ID)
	writeData(w, http.StatusAccepted, struct {
		JobID     string `json:"job_id"`
		StatusURL string `json:"status_url"`
	}{JobID: jobID, StatusURL: "/admin/jobs/" + jobID})
}
