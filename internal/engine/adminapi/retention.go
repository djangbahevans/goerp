package adminapi

import (
	"net/http"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/jobqueue"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
)

func RegisterRetentionRoutes(mux *http.ServeMux, client *river.Client[pgx.Tx]) {
	mux.HandleFunc("POST /admin/retention/preview", func(w http.ResponseWriter, r *http.Request) {
		result, err := client.Insert(r.Context(), jobqueue.RetentionSweepArgs{At: time.Now().UTC(), Preview: true}, nil)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal", "enqueue retention preview: "+err.Error())
			return
		}

		id := jobqueue.EncodeJobID(result.Job.ID)
		writeData(w, http.StatusAccepted, struct {
			JobID     string `json:"job_id"`
			StatusURL string `json:"status_url"`
		}{JobID: id, StatusURL: "/admin/jobs/" + id})
	})
}
