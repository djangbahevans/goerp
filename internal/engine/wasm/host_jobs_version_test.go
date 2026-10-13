package wasm

import (
	"database/sql"
	"encoding/json/v2"
	"slices"
	"testing"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/djangbahevans/goerp/internal/engine/jobqueue"
	"github.com/djangbahevans/goerp/internal/engine/providerselect"
)

func requireJobModuleVersion(t *testing.T, conn *sql.DB, jobID, version string) {
	t.Helper()

	id, err := jobqueue.DecodeJobID(jobID)
	if err != nil {
		t.Fatal(err)
	}

	var raw []byte
	if err := conn.QueryRow(`SELECT array_to_json(tags) FROM system.river_job WHERE id = $1`, id).Scan(&raw); err != nil {
		t.Fatalf("read job tags: %v", err)
	}

	var tags []string
	if err := json.Unmarshal(raw, &tags); err != nil {
		t.Fatalf("decode job tags: %v", err)
	}
	if want := []string{jobqueue.ModuleVersionTag(version)}; !slices.Equal(tags, want) {
		t.Fatalf("job tags = %v, want %v", tags, want)
	}
}

func TestHostJobs_EnqueueProvider_UnavailableVersion(t *testing.T) {
	f := newProviderJobsFixture(t, abi.CapJobsEnqueue)
	f.install(t, "connector_twilio", providerselect.CategorySMS, true)
	delete(f.versions, "connector_twilio")

	env := f.enqueue(t, abiv1.JobsEnqueueProviderInput{Category: providerselect.CategorySMS, JobType: "sms_send"})
	requireHostErrorCode(t, env, abiv1.ErrCodeUnavailable)
	if !env.Error.Retry {
		t.Fatal("unavailable provider version must remain retryable")
	}
	if got := countWASMJobs(t, f.conn, f.tenantID); got != 0 {
		t.Fatalf("job count = %d, want 0", got)
	}
}
