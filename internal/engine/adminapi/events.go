package adminapi

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/eventdelivery"
	"github.com/djangbahevans/goerp/internal/engine/jobqueue"
	"github.com/djangbahevans/goerp/internal/engine/registry"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
)

// replayConfirmPhrase is the literal, non-guessable value cli-reference.md
// §2a requires for a Tier 2 platform-wide operation that has no single
// target to name (unlike tenant offboard's own confirm-by-slug pattern).
const replayConfirmPhrase = "REPLAY EVENTS"

// defaultReplayBatchSize matches cli-reference.md §8's documented
// `--batch-size` default.
const defaultReplayBatchSize = 100

type EventsDeps struct {
	ModuleRegistry *registry.ModuleRegistry
	TenantStore    *tenant.Store
	Pool           *sql.DB
	JobClient      *river.Client[pgx.Tx]
}

func RegisterEventsRoutes(mux *http.ServeMux, deps EventsDeps) {
	h := &eventsHandlers{deps: deps}
	mux.HandleFunc("POST /admin/events/replay", h.replay)
}

type eventsHandlers struct {
	deps EventsDeps
}

// eventIDPrefix/encodeEventsJobID mirror jobs.go's own job_<int> encoding
// — copied rather than imported, matching offboarder.go's established
// "not worth a cross-package dependency to reuse" precedent for this
// same ~10-line helper.
const eventIDPrefix = "job_"

func encodeEventsJobID(id int64) string {
	return eventIDPrefix + strconv.FormatInt(id, 10)
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

	// Broad-target rule (cli-reference.md §2a): omitting --subscriber
	// replays to every current subscriber, and --tenant "all" spans
	// every active tenant — either makes this the "footgun" case Tier 2's
	// confirm-by-value gate exists to catch.
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

	jobID := encodeEventsJobID(result.Job.ID)
	writeData(w, http.StatusAccepted, struct {
		JobID     string `json:"job_id"`
		StatusURL string `json:"status_url"`
	}{JobID: jobID, StatusURL: "/admin/jobs/" + jobID})
}
