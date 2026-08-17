package adminapi

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
)

type JobsDeps struct {
	Client *river.Client[*sql.Tx]
}

func RegisterJobsRoutes(mux *http.ServeMux, deps JobsDeps) {
	h := &jobsHandlers{deps: deps}
	mux.HandleFunc("GET /admin/jobs", h.list)
	mux.HandleFunc("GET /admin/jobs/{id}", h.show)
	mux.HandleFunc("POST /admin/jobs/{id}/retry", h.retry)
	mux.HandleFunc("POST /admin/jobs/{id}/cancel", h.cancel)
}

type jobsHandlers struct {
	deps JobsDeps
}

// jobIDPrefix and encodeJobID/decodeJobID are the only place River's real,
// internal int64 job ID is translated to and from the wire format —
// job_<int>, a string per cli-reference.md's "IDs are strings" convention,
// but not a synthetic ULID: there's no mapping table or generation step,
// just a prefix around River's own sequence value.
const jobIDPrefix = "job_"

func encodeJobID(id int64) string {
	return jobIDPrefix + strconv.FormatInt(id, 10)
}

func decodeJobID(s string) (int64, error) {
	n, ok := strings.CutPrefix(s, jobIDPrefix)
	if !ok {
		return 0, fmt.Errorf("job id %q missing %q prefix", s, jobIDPrefix)
	}
	id, err := strconv.ParseInt(n, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("job id %q is not numeric: %w", s, err)
	}
	return id, nil
}

type jobView struct {
	ID          string             `json:"id"`
	Kind        string             `json:"kind"`
	Queue       string             `json:"queue"`
	State       rivertype.JobState `json:"state"`
	Attempt     int                `json:"attempt"`
	MaxAttempts int                `json:"max_attempts"`
	Priority    int                `json:"priority"`
	CreatedAt   time.Time          `json:"created_at"`
	ScheduledAt time.Time          `json:"scheduled_at"`
	Tags        []string           `json:"tags"`
}

func newJobView(row *rivertype.JobRow) jobView {
	return jobView{
		ID:          encodeJobID(row.ID),
		Kind:        row.Kind,
		Queue:       row.Queue,
		State:       row.State,
		Attempt:     row.Attempt,
		MaxAttempts: row.MaxAttempts,
		Priority:    row.Priority,
		CreatedAt:   row.CreatedAt,
		ScheduledAt: row.ScheduledAt,
		Tags:        row.Tags,
	}
}

// jobDetailView adds the fields jobs show wants but jobs list doesn't need
// to pay the encoding cost for on every row: Errors doubles as a basic,
// no-new-infrastructure --logs (River already tracks one AttemptError per
// failed attempt; there is no separate job-logging subsystem to draw on).
type jobDetailView struct {
	jobView
	Errors []rivertype.AttemptError `json:"errors"`
}

func newJobDetailView(row *rivertype.JobRow) jobDetailView {
	return jobDetailView{jobView: newJobView(row), Errors: row.Errors}
}

func (h *jobsHandlers) list(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	limit := 50
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			writeError(w, http.StatusBadRequest, "invalid_request", "limit must be a positive integer")
			return
		}
		limit = n
	}

	params := river.NewJobListParams().First(limit).OrderBy(river.JobListOrderByTime, river.SortOrderDesc)
	if v := q.Get("queue"); v != "" {
		params = params.Queues(v)
	}
	if v := q.Get("type"); v != "" {
		params = params.Kinds(v)
	}
	if v := q.Get("status"); v != "" {
		params = params.States(rivertype.JobState(v))
	}
	since := time.Hour
	if v := q.Get("since"); v != "" {
		parsed, err := time.ParseDuration(v)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", "since must be a duration string like \"1h\" or \"30m\"")
			return
		}
		since = parsed
	}
	params = params.Where("created_at >= @since", river.NamedArgs{"since": time.Now().Add(-since)})

	if v := q.Get("tenant"); v != "" {
		// @> containment match against InsertOpts.Metadata — only
		// job types that set tenant_id in their own Metadata are
		// filterable this way; nothing currently does (goerp#15's
		// placeholder job type isn't tenant-scoped), so this filter
		// is a working, forward-compatible mechanism that returns
		// nothing until a real tenant-scoped job type sets it.
		metadata, err := json.Marshal(map[string]string{"tenant_id": v})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal", err.Error())
			return
		}
		params = params.Metadata(string(metadata))
	}

	result, err := h.deps.Client.JobList(r.Context(), params)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}

	views := make([]jobView, len(result.Jobs))
	for i, row := range result.Jobs {
		views[i] = newJobView(row)
	}
	writeData(w, http.StatusOK, views)
}

func (h *jobsHandlers) show(w http.ResponseWriter, r *http.Request) {
	id, err := decodeJobID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}

	row, err := h.deps.Client.JobGet(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "job not found")
		return
	}

	writeData(w, http.StatusOK, newJobDetailView(row))
}

func (h *jobsHandlers) retry(w http.ResponseWriter, r *http.Request) {
	id, err := decodeJobID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}

	row, err := h.deps.Client.JobRetry(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "job not found")
		return
	}

	writeData(w, http.StatusOK, newJobView(row))
}

type cancelJobRequest struct {
	Reason string `json:"reason"`
}

func (h *jobsHandlers) cancel(w http.ResponseWriter, r *http.Request) {
	id, err := decodeJobID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}

	// Body is optional — reason is audited (adminapi's audit middleware
	// reads the same cached body) but not required by River's own
	// JobCancel, which has no reason parameter of its own.
	_, _ = decodeJSON[cancelJobRequest](r)

	row, err := h.deps.Client.JobCancel(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "job not found")
		return
	}

	writeData(w, http.StatusOK, newJobView(row))
}
