package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTenantOffboard_GracePeriodSuccess(t *testing.T) {
	var gotPath string
	var gotBody struct {
		GracePeriod string `json:"grace_period"`
		Immediate   bool   `json:"immediate"`
		Confirm     string `json:"confirm"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"status":"scheduled","delete_at":"2026-09-20T12:00:00Z"},"error":null}`))
	}))
	defer srv.Close()

	code, stdout, stderr := runCLI(t, "tenant", "offboard", "acmecorp",
		"--grace-period", "30d",
		"--confirm", "acmecorp",
		"--admin-url", srv.URL,
		"--admin-token", "testtoken",
	)

	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr)
	}
	if gotPath != "/admin/tenants/acmecorp/offboard" {
		t.Errorf("request path = %q, want /admin/tenants/acmecorp/offboard", gotPath)
	}
	if gotBody.GracePeriod != "30d" {
		t.Errorf("request grace_period = %q, want %q", gotBody.GracePeriod, "30d")
	}
	if gotBody.Immediate {
		t.Error("request immediate = true, want false")
	}
	if gotBody.Confirm != "acmecorp" {
		t.Errorf("request confirm = %q, want %q", gotBody.Confirm, "acmecorp")
	}
	if !strings.Contains(stdout, "scheduled for deletion") {
		t.Errorf("stdout = %q, want it to mention the scheduled deletion", stdout)
	}
	if !strings.Contains(stdout, "offboard cancel acmecorp") {
		t.Errorf("stdout = %q, want it to mention how to cancel", stdout)
	}
}

func TestTenantOffboard_ImmediateSuccess(t *testing.T) {
	var gotBody struct {
		Immediate bool `json:"immediate"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusAccepted)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"status":"accepted","job_id":"job_42"},"error":null}`))
	}))
	defer srv.Close()

	code, stdout, stderr := runCLI(t, "tenant", "offboard", "test-tenant",
		"--immediate",
		"--confirm", "test-tenant",
		"--admin-url", srv.URL,
		"--admin-token", "testtoken",
	)

	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr)
	}
	if !gotBody.Immediate {
		t.Error("request immediate = false, want true")
	}
	if !strings.Contains(stdout, "job_42") {
		t.Errorf("stdout = %q, want it to mention the job id", stdout)
	}
	if !strings.Contains(stdout, "jobs show job_42") {
		t.Errorf("stdout = %q, want it to mention how to check job progress", stdout)
	}
}

func TestTenantOffboard_JSONOutput(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"status":"scheduled","delete_at":"2026-09-20T12:00:00Z"},"error":null}`))
	}))
	defer srv.Close()

	code, stdout, _ := runCLI(t, "tenant", "offboard", "acmecorp",
		"--confirm", "acmecorp",
		"--admin-url", srv.URL,
		"--admin-token", "testtoken",
		"--json",
	)

	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}

	var data struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &data); err != nil {
		t.Fatalf("stdout %q is not the raw data envelope: %v", stdout, err)
	}
	if data.Status != "scheduled" {
		t.Errorf("Status = %q, want %q", data.Status, "scheduled")
	}
}

func TestTenantOffboard_MissingConfirmIsUsageError(t *testing.T) {
	code, _, stderr := runCLI(t, "tenant", "offboard", "acmecorp",
		"--admin-url", "http://127.0.0.1:1",
		"--admin-token", "testtoken",
	)

	if code != 2 {
		t.Fatalf("exit code = %d, want 2 (usage error)", code)
	}
	if !strings.Contains(stderr, "confirm") {
		t.Errorf("stderr = %q, want it to mention --confirm", stderr)
	}
}

func TestTenantOffboard_WrongConfirmIsUsageError(t *testing.T) {
	code, _, stderr := runCLI(t, "tenant", "offboard", "acmecorp",
		"--confirm", "wrong-slug",
		"--admin-url", "http://127.0.0.1:1",
		"--admin-token", "testtoken",
	)

	if code != 2 {
		t.Fatalf("exit code = %d, want 2 (usage error)", code)
	}
	if !strings.Contains(stderr, "confirm") {
		t.Errorf("stderr = %q, want it to mention --confirm", stderr)
	}
}

func TestTenantOffboard_InvalidGracePeriodIsUsageError(t *testing.T) {
	code, _, stderr := runCLI(t, "tenant", "offboard", "acmecorp",
		"--grace-period", "not-a-duration",
		"--confirm", "acmecorp",
		"--admin-url", "http://127.0.0.1:1",
		"--admin-token", "testtoken",
	)

	if code != 2 {
		t.Fatalf("exit code = %d, want 2 (usage error)", code)
	}
	if !strings.Contains(stderr, "grace-period") {
		t.Errorf("stderr = %q, want it to mention --grace-period", stderr)
	}
}

func TestTenantOffboard_DryRunDoesNotRequireConfirm(t *testing.T) {
	var gotPath, gotMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotMethod = r.Method
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"slug":"acmecorp","status":"active"},"error":null}`))
	}))
	defer srv.Close()

	code, stdout, stderr := runCLI(t, "tenant", "offboard", "acmecorp",
		"--grace-period", "7d",
		"--dry-run",
		"--admin-url", srv.URL,
		"--admin-token", "testtoken",
	)

	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr)
	}
	if gotMethod != http.MethodGet {
		t.Errorf("request method = %q, want GET (dry-run must not mutate)", gotMethod)
	}
	if gotPath != "/admin/tenants/acmecorp" {
		t.Errorf("request path = %q, want /admin/tenants/acmecorp", gotPath)
	}
	if !strings.Contains(stdout, "would offboard") {
		t.Errorf("stdout = %q, want a dry-run preview", stdout)
	}
	if !strings.Contains(stdout, "would delete") {
		t.Errorf("stdout = %q, want the deletion scope listed", stdout)
	}
}

func TestTenantOffboard_DryRunImmediate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"slug":"acmecorp","status":"active"},"error":null}`))
	}))
	defer srv.Close()

	code, stdout, stderr := runCLI(t, "tenant", "offboard", "acmecorp",
		"--immediate",
		"--dry-run",
		"--admin-url", srv.URL,
		"--admin-token", "testtoken",
	)

	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr)
	}
	if !strings.Contains(stdout, "immediately") {
		t.Errorf("stdout = %q, want it to mention immediate deletion", stdout)
	}
}

func TestTenantOffboard_DryRunNotFoundIsExitCode4(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"data":null,"error":{"code":"not_found","message":"tenant not found"}}`))
	}))
	defer srv.Close()

	code, _, stderr := runCLI(t, "tenant", "offboard", "does-not-exist",
		"--dry-run",
		"--admin-url", srv.URL,
		"--admin-token", "testtoken",
	)

	if code != 4 {
		t.Fatalf("exit code = %d, want 4 (not found, cli-reference.md §2b)", code)
	}
	if !strings.Contains(stderr, "not_found") {
		t.Errorf("stderr = %q, want it to mention the not_found API error code", stderr)
	}
}

func TestTenantOffboard_MissingAdminTokenIsUsageError(t *testing.T) {
	code, _, stderr := runCLI(t, "tenant", "offboard", "acmecorp",
		"--confirm", "acmecorp",
		"--admin-url", "http://127.0.0.1:1",
	)

	if code != 2 {
		t.Fatalf("exit code = %d, want 2 (usage error)", code)
	}
	if !strings.Contains(stderr, "admin-url") && !strings.Contains(stderr, "admin-token") {
		t.Errorf("stderr = %q, want it to mention the missing admin credential pair", stderr)
	}
}

func TestTenantOffboardCancel_Success(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"status":"cancelled"},"error":null}`))
	}))
	defer srv.Close()

	code, stdout, stderr := runCLI(t, "tenant", "offboard", "cancel", "acmecorp",
		"--admin-url", srv.URL,
		"--admin-token", "testtoken",
	)

	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr)
	}
	if gotPath != "/admin/tenants/acmecorp/offboard/cancel" {
		t.Errorf("request path = %q, want /admin/tenants/acmecorp/offboard/cancel", gotPath)
	}
	if !strings.Contains(stdout, "restored to active") {
		t.Errorf("stdout = %q, want it to mention the tenant was restored", stdout)
	}
}

// TestTenantOffboardCancel_NothingToCancelIsExitCode1 matches the real
// offboardCancel handler's behavior (internal/engine/adminapi/tenant.go):
// it reports a failed CancelOffboard as 400 Bad Request ("invalid_request"),
// not 409 Conflict, so exitCodeForStatus's default branch (1) applies —
// same status the handler already uses for the --confirm mismatch case.
func TestTenantOffboardCancel_NothingToCancelIsExitCode1(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"data":null,"error":{"code":"invalid_request","message":"tenant offboard is not cancellable"}}`))
	}))
	defer srv.Close()

	code, _, stderr := runCLI(t, "tenant", "offboard", "cancel", "acmecorp",
		"--admin-url", srv.URL,
		"--admin-token", "testtoken",
	)

	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr, "not cancellable") {
		t.Errorf("stderr = %q, want it to mention the tenant isn't cancellable", stderr)
	}
}

func TestTenantOffboardCancel_MissingAdminTokenIsUsageError(t *testing.T) {
	code, _, stderr := runCLI(t, "tenant", "offboard", "cancel", "acmecorp",
		"--admin-url", "http://127.0.0.1:1",
	)

	if code != 2 {
		t.Fatalf("exit code = %d, want 2 (usage error)", code)
	}
	if !strings.Contains(stderr, "admin-url") && !strings.Contains(stderr, "admin-token") {
		t.Errorf("stderr = %q, want it to mention the missing admin credential pair", stderr)
	}
}
