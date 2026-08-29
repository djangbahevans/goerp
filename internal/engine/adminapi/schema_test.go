package adminapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/schema"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	tenantsync "github.com/djangbahevans/goerp/internal/engine/tenant/sync"
)

type fakeSchemaStatusReader struct {
	gotTenant, gotModule, gotFilter string
	result                          []schema.TenantModuleStatus
	err                             error
}

func (f *fakeSchemaStatusReader) Status(ctx context.Context, tenantSlug, moduleName, filter string) ([]schema.TenantModuleStatus, error) {
	f.gotTenant, f.gotModule, f.gotFilter = tenantSlug, moduleName, filter
	return f.result, f.err
}

type fakeSchemaDiffer struct {
	gotTenant, gotModule    string
	gotVerbose              bool
	version                 string
	safe, deferred, blocked []schema.ChangeSummary
	err                     error
}

func (f *fakeSchemaDiffer) Diff(ctx context.Context, tenantSlug, moduleName string, verbose bool) (string, []schema.ChangeSummary, []schema.ChangeSummary, []schema.ChangeSummary, error) {
	f.gotTenant, f.gotModule, f.gotVerbose = tenantSlug, moduleName, verbose
	return f.version, f.safe, f.deferred, f.blocked, f.err
}

type fakeSchemaSyncer struct {
	gotTenant, gotModule string
	gotScheduledAt       *time.Time
	jobID                string
	err                  error
}

func (f *fakeSchemaSyncer) StartSync(ctx context.Context, tenantSlug, moduleName string, scheduledAt *time.Time) (string, error) {
	f.gotTenant, f.gotModule, f.gotScheduledAt = tenantSlug, moduleName, scheduledAt
	return f.jobID, f.err
}

type fakeSchemaAccepter struct {
	gotTenant, gotModule, gotReason, gotOperator string
	acceptanceIDs                                []string
	jobID                                        string
	err                                          error
}

func (f *fakeSchemaAccepter) Accept(ctx context.Context, tenantSlug, moduleName, reason, operator string) ([]string, string, error) {
	f.gotTenant, f.gotModule, f.gotReason, f.gotOperator = tenantSlug, moduleName, reason, operator
	return f.acceptanceIDs, f.jobID, f.err
}

func TestSchemaStatusRoute_PassesQueryParamsAndReturnsData(t *testing.T) {
	fake := &fakeSchemaStatusReader{result: []schema.TenantModuleStatus{{TenantSlug: "acme", ModuleName: "sales", Status: "ok"}}}
	mux := http.NewServeMux()
	RegisterSchemaRoutes(mux, SchemaDeps{Status: fake})

	req := httptest.NewRequest(http.MethodGet, "/admin/schema/status?tenant=acme&module=sales&filter=ok", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if fake.gotTenant != "acme" || fake.gotModule != "sales" || fake.gotFilter != "ok" {
		t.Errorf("Status called with (%q, %q, %q), want (acme, sales, ok)", fake.gotTenant, fake.gotModule, fake.gotFilter)
	}
}

func TestSchemaDiffRoute_RequiresTenant(t *testing.T) {
	mux := http.NewServeMux()
	RegisterSchemaRoutes(mux, SchemaDeps{Diff: &fakeSchemaDiffer{}})

	req := httptest.NewRequest(http.MethodGet, "/admin/modules/sales/schema", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestSchemaDiffRoute_ReturnsClassifiedChanges(t *testing.T) {
	fake := &fakeSchemaDiffer{
		version: "1.1.0",
		blocked: []schema.ChangeSummary{{Kind: "drop_column", Table: "widgets", Detail: "sku", Hash: "abc123"}},
	}
	mux := http.NewServeMux()
	RegisterSchemaRoutes(mux, SchemaDeps{Diff: fake})

	req := httptest.NewRequest(http.MethodGet, "/admin/modules/sales/schema?tenant=acme", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if fake.gotTenant != "acme" || fake.gotModule != "sales" {
		t.Errorf("Diff called with (%q, %q), want (acme, sales)", fake.gotTenant, fake.gotModule)
	}

	var env struct {
		Data schemaDiffResponse `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if env.Data.Version != "1.1.0" || len(env.Data.Blocked) != 1 || env.Data.Blocked[0].Hash != "abc123" {
		t.Errorf("response = %+v, want version 1.1.0 with one blocked change carrying hash abc123", env.Data)
	}
}

func TestSchemaDiffRoute_PassesVerboseThrough(t *testing.T) {
	fake := &fakeSchemaDiffer{}
	mux := http.NewServeMux()
	RegisterSchemaRoutes(mux, SchemaDeps{Diff: fake})

	req := httptest.NewRequest(http.MethodGet, "/admin/modules/sales/schema?tenant=acme&verbose=true", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if !fake.gotVerbose {
		t.Error("Diff called with verbose = false, want true")
	}
}

func TestSchemaDiffRoute_UnknownTenantIsNotFound(t *testing.T) {
	fake := &fakeSchemaDiffer{err: fmt.Errorf("look up tenant %q: %w", "acme", tenant.ErrTenantNotFound)}
	mux := http.NewServeMux()
	RegisterSchemaRoutes(mux, SchemaDeps{Diff: fake})

	req := httptest.NewRequest(http.MethodGet, "/admin/modules/sales/schema?tenant=acme", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d (body: %s)", w.Code, http.StatusNotFound, w.Body.String())
	}
}

func TestSchemaDiffRoute_UnloadedModuleIsNotFound(t *testing.T) {
	fake := &fakeSchemaDiffer{err: fmt.Errorf("module %q: %w", "sales", tenantsync.ErrModuleNotLoaded)}
	mux := http.NewServeMux()
	RegisterSchemaRoutes(mux, SchemaDeps{Diff: fake})

	req := httptest.NewRequest(http.MethodGet, "/admin/modules/sales/schema?tenant=acme", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d (body: %s)", w.Code, http.StatusNotFound, w.Body.String())
	}
}

func TestSchemaSyncRoute_ParsesScheduleAndReturnsJobID(t *testing.T) {
	fake := &fakeSchemaSyncer{jobID: "job_42"}
	mux := http.NewServeMux()
	RegisterSchemaRoutes(mux, SchemaDeps{Sync: fake})

	req := httptest.NewRequest(http.MethodPost, "/admin/schema/sync", strings.NewReader(`{"tenant":"acme","module":"sales","schedule":"2026-05-09T02:00:00Z"}`))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if fake.gotTenant != "acme" || fake.gotModule != "sales" {
		t.Errorf("StartSync called with (%q, %q), want (acme, sales)", fake.gotTenant, fake.gotModule)
	}
	if fake.gotScheduledAt == nil || !fake.gotScheduledAt.Equal(time.Date(2026, 5, 9, 2, 0, 0, 0, time.UTC)) {
		t.Errorf("gotScheduledAt = %v, want 2026-05-09T02:00:00Z", fake.gotScheduledAt)
	}
}

func TestSchemaSyncRoute_ZonelessScheduleIsBadRequest(t *testing.T) {
	mux := http.NewServeMux()
	RegisterSchemaRoutes(mux, SchemaDeps{Sync: &fakeSchemaSyncer{}})

	req := httptest.NewRequest(http.MethodPost, "/admin/schema/sync", strings.NewReader(`{"schedule":"2026-05-09T02:00:00"}`))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d (body: %s)", w.Code, http.StatusBadRequest, w.Body.String())
	}
}

func TestSchemaAcceptRoute_MissingFieldsIsBadRequest(t *testing.T) {
	mux := http.NewServeMux()
	RegisterSchemaRoutes(mux, SchemaDeps{Accept: &fakeSchemaAccepter{}})

	req := httptest.NewRequest(http.MethodPost, "/admin/schema/accept", strings.NewReader(`{"module":"sales"}`))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestSchemaAcceptRoute_SingleAcceptancePopulatesSingularAndPluralFields(t *testing.T) {
	fake := &fakeSchemaAccepter{acceptanceIDs: []string{"acc-1"}, jobID: "job_7"}
	mux := http.NewServeMux()
	RegisterSchemaRoutes(mux, SchemaDeps{Accept: fake})

	req := httptest.NewRequest(http.MethodPost, "/admin/schema/accept", strings.NewReader(`{"module":"sales","tenant":"acme","reason":"verified"}`))
	req.Header.Set("X-GoERP-Operator-Identity", "alice")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if fake.gotOperator != "alice" {
		t.Errorf("gotOperator = %q, want %q", fake.gotOperator, "alice")
	}

	var env struct {
		Data struct {
			JobID         string   `json:"job_id"`
			AcceptanceID  string   `json:"acceptance_id"`
			AcceptanceIDs []string `json:"acceptance_ids"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if env.Data.AcceptanceID != "acc-1" {
		t.Errorf("acceptance_id = %q, want %q", env.Data.AcceptanceID, "acc-1")
	}
	if len(env.Data.AcceptanceIDs) != 1 || env.Data.AcceptanceIDs[0] != "acc-1" {
		t.Errorf("acceptance_ids = %v, want [acc-1] — always populated, not just when there's more than one", env.Data.AcceptanceIDs)
	}
}

func TestSchemaAcceptRoute_NothingBlockedIsConflict(t *testing.T) {
	fake := &fakeSchemaAccepter{err: tenantsync.ErrNothingBlocked}
	mux := http.NewServeMux()
	RegisterSchemaRoutes(mux, SchemaDeps{Accept: fake})

	req := httptest.NewRequest(http.MethodPost, "/admin/schema/accept", strings.NewReader(`{"module":"sales","tenant":"acme","reason":"verified"}`))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Errorf("status = %d, want %d (body: %s)", w.Code, http.StatusConflict, w.Body.String())
	}
}
