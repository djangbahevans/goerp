package adminapi

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"net/http"
	"strconv"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/jobqueue"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
)

type JobsDeps struct {
	Client *river.Client[pgx.Tx]
	// OutputDecryptor optionally transforms a completed job's raw Output
	// before show returns it to a poller — e.g. tenant.export's Result
	// carries its one-time archive decryption key rowcrypt-encrypted at
	// rest (goerp#453), and this is where it's decrypted back for the
	// legitimate, already-admin-authenticated caller polling for it. Kept
	// generic (dispatches on kind itself) rather than jobs.go knowing
	// about any specific job kind's Result shape. A nil OutputDecryptor
	// (or one that returns output unchanged for a kind it doesn't
	// recognize) leaves Output exactly as recorded.
	//
	// jsontext.Value here is binary-compatible with the json.RawMessage
	// callers elsewhere (internal/engine.go, tenantexport.DecryptOutput)
	// still use — Go 1.27 makes RawMessage a type alias for it.
	OutputDecryptor func(kind string, output jsontext.Value) (jsontext.Value, error)
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
		ID:          jobqueue.EncodeJobID(row.ID),
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
// Output surfaces whatever a Worker recorded via river.RecordOutput
// (stored under Metadata[rivertype.MetadataKeyOutput]) — e.g. goerp
// tenant export's download URL/checksum/decryption key, which has
// nowhere else to be returned to a polling CLI once the job is async.
type jobDetailView struct {
	jobView
	Errors []rivertype.AttemptError `json:"errors"`
	Output jsontext.Value           `json:"output,omitempty"`
}

func newJobDetailView(row *rivertype.JobRow) jobDetailView {
	view := jobDetailView{jobView: newJobView(row), Errors: row.Errors}
	if len(row.Metadata) > 0 {
		var metadata map[string]jsontext.Value
		if err := json.Unmarshal(row.Metadata, &metadata); err == nil {
			view.Output = metadata[rivertype.MetadataKeyOutput]
		}
	}
	return view
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
	id, err := jobqueue.DecodeJobID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}

	row, err := h.deps.Client.JobGet(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "job not found")
		return
	}

	view := newJobDetailView(row)
	if h.deps.OutputDecryptor != nil && len(view.Output) > 0 {
		decrypted, err := h.deps.OutputDecryptor(row.Kind, view.Output)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal", err.Error())
			return
		}
		view.Output = decrypted
	}

	writeData(w, http.StatusOK, view)
}

func (h *jobsHandlers) retry(w http.ResponseWriter, r *http.Request) {
	id, err := jobqueue.DecodeJobID(r.PathValue("id"))
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
	id, err := jobqueue.DecodeJobID(r.PathValue("id"))
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
