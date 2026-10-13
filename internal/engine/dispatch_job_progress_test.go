package engine

import (
	"bufio"
	"encoding/json/v2"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"uuid"

	"github.com/djangbahevans/goerp/internal/engine/jobprogress"
	"github.com/djangbahevans/goerp/internal/engine/jobqueue"
	"github.com/djangbahevans/goerp/internal/engine/sse"
)

func progressJob(t *testing.T, f *chainFixture) int64 {
	t.Helper()
	ensureRiverJobMigrated(t)
	args, _ := json.Marshal(jobqueue.WASMJobArgs{TenantID: f.tenantID, UserID: f.userID, ModuleName: "widgets", JobType: "import"})
	var id int64
	if err := f.conn.QueryRow(`INSERT INTO system.river_job (kind,args,state,queue,max_attempts,attempted_at) VALUES ('wasm_job',$1,'running',$2,3,NOW()) RETURNING id`, string(args), "progress_"+uuid.New().String()).Scan(&id); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = f.conn.Exec(`DELETE FROM system.job_progress WHERE job_id=$1`, id)
		_, _ = f.conn.Exec(`DELETE FROM system.river_job WHERE id=$1`, id)
	})
	return id
}

func nextProgressSnapshot(t *testing.T, reader *bufio.Reader) jobprogress.Snapshot {
	t.Helper()
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		if data, ok := strings.CutPrefix(line, "data: "); ok {
			var snapshot jobprogress.Snapshot
			if err := json.Unmarshal([]byte(data), &snapshot); err != nil {
				t.Fatal(err)
			}
			return snapshot
		}
	}
}

func TestJobProgressHTTPChangesAcrossStoresAndDone(t *testing.T) {
	f := newChainFixture(t)
	store := jobprogress.New(f.conn)
	if err := store.Bootstrap(t.Context()); err != nil {
		t.Fatal(err)
	}
	id := progressJob(t, f)
	e := &Engine{jobProgress: store, streams: sse.New(2, 1, time.Minute)}
	defer e.streams.Shutdown()
	handler := f.chain(map[string]http.Handler{"GET /_jobs/progress/{job_id}": http.HandlerFunc(e.dispatchJobProgressRoute)})
	server := httptest.NewServer(handler)
	defer server.Close()
	server.Client().Timeout = 8 * time.Second
	request, err := http.NewRequestWithContext(t.Context(), "GET", server.URL+"/_jobs/progress/"+jobqueue.EncodeJobID(id), nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Host = f.domain
	request.Header.Set("Authorization", "Bearer "+f.issueToken(t))
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 200 || response.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("response=%v", response)
	}
	reader := bufio.NewReader(response.Body)
	first := nextProgressSnapshot(t, reader)
	if first.JobID != jobqueue.EncodeJobID(id) || first.Status != "running" || first.Percent != 0 || first.StartedAt == nil || first.CompletedAt != nil {
		t.Fatalf("first=%+v", first)
	}

	// A separate database pool models a worker with no shared process memory.
	writer := jobprogress.New(openDispatchORMTestDB(t))
	if err := writer.Save(t.Context(), id, jobprogress.Report{Percent: 40, Message: "Imported 400", HasProgress: true}); err != nil {
		t.Fatal(err)
	}
	second := nextProgressSnapshot(t, reader)
	if second.Percent != 40 || second.Message != "Imported 400" || second.Result != nil {
		t.Fatalf("second=%+v", second)
	}
	if err := writer.Save(t.Context(), id, jobprogress.Report{Result: []byte(`{"created":1000}`)}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.conn.Exec(`UPDATE system.river_job SET state='completed',finalized_at=NOW() WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	final := nextProgressSnapshot(t, reader)
	if final.Status != "complete" || final.Percent != 100 || !strings.Contains(string(final.Result), "1000") || final.CompletedAt == nil {
		t.Fatalf("final=%+v", final)
	}
	remaining, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(remaining)) != "event: done\ndata: {}" {
		t.Fatalf("trailing stream=%q", remaining)
	}
}

func TestJobProgressHTTPRefusalsAndFinishedJobs(t *testing.T) {
	f := newChainFixture(t)
	store := jobprogress.New(f.conn)
	if err := store.Bootstrap(t.Context()); err != nil {
		t.Fatal(err)
	}
	id := progressJob(t, f)
	e := &Engine{jobProgress: store, streams: sse.New(2, 1, time.Minute)}
	defer e.streams.Shutdown()
	handler := f.chain(map[string]http.Handler{"GET /_jobs/progress/{job_id}": http.HandlerFunc(e.dispatchJobProgressRoute)})
	token := f.issueToken(t)
	request := func(rawID, auth string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", "http://"+f.domain+"/_jobs/progress/"+rawID, nil)
		if auth != "" {
			req.Header.Set("Authorization", "Bearer "+auth)
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}
	rawID := jobqueue.EncodeJobID(id)
	if rec := request(rawID, ""); rec.Code != 401 {
		t.Fatalf("anonymous status=%d", rec.Code)
	}
	for _, test := range []struct {
		name string
		args map[string]any
		kind string
	}{
		{"other user", map[string]any{"tenant_id": f.tenantID, "user_id": uuid.New().String()}, "wasm_job"},
		{"other tenant", map[string]any{"tenant_id": uuid.New().String(), "user_id": f.userID}, "wasm_job"},
		{"anonymous job", map[string]any{"tenant_id": f.tenantID}, "wasm_job"},
		{"migration", map[string]any{"tenant_id": f.tenantID, "user_id": f.userID, "is_data_migration": true}, "wasm_job"},
		{"cron", map[string]any{"tenant_id": f.tenantID, "user_id": f.userID, "is_cron": true}, "wasm_job"},
		{"delivery", map[string]any{"tenant_id": f.tenantID, "user_id": f.userID, "notification_id": "private"}, "wasm_job"},
		{"engine", map[string]any{"tenant_id": f.tenantID, "user_id": f.userID}, "tenant_export"},
	} {
		args, _ := json.Marshal(test.args)
		if _, err := f.conn.Exec(`UPDATE system.river_job SET args=$1,kind=$2 WHERE id=$3`, string(args), test.kind, id); err != nil {
			t.Fatal(err)
		}
		if rec := request(rawID, token); rec.Code != 404 || !strings.Contains(rec.Body.String(), `"code":"jobs.not_found"`) {
			t.Fatalf("%s: %d %s", test.name, rec.Code, rec.Body)
		}
	}
	for _, badID := range []string{"no-such-job", "job_0", "job_-1", "job_01", "job_9223372036854775807"} {
		if rec := request(badID, token); rec.Code != 404 || !strings.Contains(rec.Body.String(), `"code":"jobs.not_found"`) {
			t.Fatalf("%s: %d %s", badID, rec.Code, rec.Body)
		}
	}
	args, _ := json.Marshal(map[string]string{"tenant_id": f.tenantID, "user_id": f.userID})
	for _, state := range []string{"completed", "cancelled", "discarded"} {
		if _, err := f.conn.Exec(`UPDATE system.river_job SET kind='wasm_job',args=$1,state=$2,finalized_at=NOW(),errors=ARRAY['{"error":"private handler failure"}'::jsonb] WHERE id=$3`, string(args), state, id); err != nil {
			t.Fatal(err)
		}
		rec := request(rawID, token)
		if rec.Code != 200 || strings.Count(rec.Body.String(), `"job_id"`) != 1 || !strings.Contains(rec.Body.String(), "event: done") || strings.Contains(rec.Body.String(), "private handler") {
			t.Fatalf("%s: %d %s", state, rec.Code, rec.Body)
		}
		if state == "cancelled" && !strings.Contains(rec.Body.String(), "jobs.cancelled") {
			t.Fatal(rec.Body)
		}
		if state == "discarded" && !strings.Contains(rec.Body.String(), "jobs.failed") {
			t.Fatal(rec.Body)
		}
	}
	if _, err := f.conn.Exec(`DELETE FROM system.river_job WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	if rec := request(rawID, token); rec.Code != 404 {
		t.Fatalf("pruned job status=%d", rec.Code)
	}
}

func TestJobFailureMessages(t *testing.T) {
	for _, language := range []string{"en", "fr-CA", "ar", "de"} {
		if jobFailureMessage(language, "jobs.failed") == jobFailureMessage(language, "jobs.cancelled") {
			t.Fatalf("%s: failure messages indistinguishable", language)
		}
	}
	if got := jobFailureMessage("fr-CA", "jobs.cancelled"); got != "Le travail a été annulé" {
		t.Fatal(got)
	}
}
