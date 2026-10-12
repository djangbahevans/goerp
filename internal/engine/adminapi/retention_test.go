package adminapi

import (
	"context"
	"encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/config"
	"github.com/djangbahevans/goerp/internal/engine/jobqueue"
	"github.com/djangbahevans/goerp/internal/engine/jobqueue/jobqueuetest"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/registry"
	"github.com/djangbahevans/goerp/internal/engine/retention"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdbtest"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
)

func TestRetentionPreviewRouteAndJobOutput(t *testing.T) {
	pool, err := pgxpool.New(t.Context(), jobsTestDSN)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := pool.Ping(t.Context()); err != nil {
		t.Skipf("dev Postgres unreachable: %v", err)
	}

	reg := &registry.ModuleRegistry{}
	if _, err := reg.Update(map[string]*module.LoadedModule{}); err != nil {
		t.Fatal(err)
	}
	workers := river.NewWorkers()
	river.AddWorker(workers, &retention.Worker{Sweeper: &retention.Sweeper{Registry: reg}})
	driver := riverpgxv5.New(pool)
	schema := riverdbtest.TestSchema(t.Context(), t, driver, &riverdbtest.TestSchemaOpts{DisableReuse: true})
	client, err := jobqueuetest.New(driver, schema, &config.Config{}, workers)
	if err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	RegisterRetentionRoutes(mux, client)
	RegisterJobsRoutes(mux, JobsDeps{Client: client})
	start := time.Now()
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/admin/retention/preview", nil))
	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}

	var response struct {
		Data struct {
			JobID     string `json:"job_id"`
			StatusURL string `json:"status_url"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Data.StatusURL != "/admin/jobs/"+response.Data.JobID {
		t.Errorf("response = %+v", response)
	}
	id, err := jobqueue.DecodeJobID(response.Data.JobID)
	if err != nil {
		t.Fatal(err)
	}
	row, err := client.JobGet(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	var args jobqueue.RetentionSweepArgs
	if err := json.Unmarshal(row.EncodedArgs, &args); err != nil {
		t.Fatal(err)
	}
	if row.Kind != "retention_sweep" || row.Queue != jobqueue.QueueAdmin || !args.Preview || args.At.Before(start) || args.At.After(time.Now()) {
		t.Fatalf("job = %+v, args = %+v", row, args)
	}

	if err := client.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := client.Stop(ctx); err != nil {
			t.Errorf("stop retention test client: %v", err)
		}
	})
	waitForJobCompleted(t, client, id)

	w = httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, response.Data.StatusURL, nil))
	if w.Code != http.StatusOK {
		t.Fatalf("job status = %d, body = %s", w.Code, w.Body.String())
	}
	var detail struct {
		Data struct {
			Output retention.Result `json:"output"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &detail); err != nil {
		t.Fatal(err)
	}
	if !detail.Data.Output.Preview || !detail.Data.Output.At.Equal(args.At) || detail.Data.Output.Policies == nil {
		t.Errorf("job output = %+v", detail.Data.Output)
	}
}
