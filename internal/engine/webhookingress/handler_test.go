package webhookingress

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/connectoringress"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/route"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	"github.com/djangbahevans/goerp/internal/engine/wasm"
	"github.com/vmihailenco/msgpack/v5"
)

const (
	testToken    = "0123456789abcdefghijklmnopqrstuvwxyzABCDEFG" // 43 base62 characters
	testTenantID = "tenant-1"
	testModule   = "connector_paystack"
)

type fakeEndpoints struct {
	tenantID string
	err      error
	calls    int
}

func (f *fakeEndpoints) ResolveEndpoint(_ context.Context, token, moduleName string) (string, error) {
	f.calls++
	if f.err != nil {
		return "", f.err
	}
	if token != testToken || moduleName != testModule {
		return "", connectoringress.ErrEndpointNotFound
	}
	return f.tenantID, nil
}

type accepted struct {
	tenantID, module, eventID string
	payload                   []byte
}

type fakeInbox struct {
	seen    map[string]bool
	accepts []accepted
	err     error
}

func (f *fakeInbox) AcceptDelivery(ctx context.Context, tenantID, moduleName, eventID string, payload []byte, enqueue func(context.Context, *sql.Tx, string) error) (string, bool, error) {
	if f.err != nil {
		return "", false, f.err
	}
	key := tenantID + "/" + moduleName + "/" + eventID
	if f.seen[key] {
		return "", false, nil
	}
	if err := enqueue(ctx, nil, "inbox-"+eventID); err != nil {
		return "", false, err
	}
	f.seen[key] = true
	f.accepts = append(f.accepts, accepted{tenantID, moduleName, eventID, payload})
	return "inbox-" + eventID, true, nil
}

type fakeTenants struct {
	tenant *tenant.Tenant
	err    error
}

func (f *fakeTenants) GetByID(context.Context, string) (*tenant.Tenant, error) {
	return f.tenant, f.err
}

type fakeConfig struct {
	values    map[string]string
	encrypted map[string]bool
}

func (f *fakeConfig) Get(_ context.Context, _, key string) (string, bool, bool, error) {
	v, ok := f.values[key]
	return v, f.encrypted[key], ok, nil
}

type fakeVerifier struct {
	verdict wasm.WebhookVerdict
	got     abiv1.WebhookVerifyRequest
	calls   int
}

func (f *fakeVerifier) VerifyWebhook(_ context.Context, _ wasm.WebhookVerifierSource, req abiv1.WebhookVerifyRequest) wasm.WebhookVerdict {
	f.calls++
	f.got = req
	return f.verdict
}

type fakeEnqueuer struct {
	jobs []enqueued
	err  error
}

type enqueued struct {
	tenantID, module, jobType string
	payload                   []byte
}

func (f *fakeEnqueuer) EnqueueModuleJobTx(_ context.Context, _ *sql.Tx, tenantID, moduleName string, jt manifest.JobType, payload []byte) error {
	if f.err != nil {
		return f.err
	}
	f.jobs = append(f.jobs, enqueued{tenantID, moduleName, jt.Name, payload})
	return nil
}

type fakeRedis struct {
	allow      bool
	retryAfter time.Duration
	err        error
	windowKeys []string
	incrKeys   []string
	counts     map[string]int64
}

func (f *fakeRedis) SlidingWindowAllow(_ context.Context, key string, limit int, window time.Duration) (bool, time.Duration, error) {
	f.windowKeys = append(f.windowKeys, key)
	if limit != RateLimit || window != RateWindow {
		return false, 0, errors.New("unexpected limit")
	}
	return f.allow || f.err != nil, f.retryAfter, f.err
}

func (f *fakeRedis) IncrWithTTL(_ context.Context, key string, _ time.Duration) (int64, error) {
	f.incrKeys = append(f.incrKeys, key)
	if f.counts == nil {
		f.counts = map[string]int64{}
	}
	f.counts[key]++
	return f.counts[key], nil
}

func (f *fakeRedis) Get(_ context.Context, key string) (string, bool, error) {
	n, ok := f.counts[key]
	return strconv.FormatInt(n, 10), ok, nil
}

type env struct {
	h         *Handler
	endpoints *fakeEndpoints
	inbox     *fakeInbox
	tenants   *fakeTenants
	config    *fakeConfig
	verifier  *fakeVerifier
	enqueuer  *fakeEnqueuer
	redis     *fakeRedis
	connector Connector
	available bool
	known     bool
}

func newEnv() *env {
	e := &env{
		endpoints: &fakeEndpoints{tenantID: testTenantID},
		inbox:     &fakeInbox{seen: map[string]bool{}},
		tenants:   &fakeTenants{tenant: &tenant.Tenant{ID: testTenantID, Status: tenant.StatusActive}},
		config: &fakeConfig{
			values:    map[string]string{testModule + ".webhook_secret": "current"},
			encrypted: map[string]bool{},
		},
		verifier: &fakeVerifier{verdict: wasm.WebhookVerdict{Outcome: wasm.WebhookAccepted, ProviderEventID: "evt_1"}},
		enqueuer: &fakeEnqueuer{},
		redis:    &fakeRedis{allow: true},
		connector: Connector{
			Name: testModule, MediaType: "application/json", HasVerifier: true,
			InboxJob: &manifest.JobType{Name: "paystack_process_inbox"},
		},
		available: true,
		known:     true,
	}
	e.h = NewHandler(Deps{
		Connector: func(name string) (Connector, bool, bool) {
			if name != testModule {
				return Connector{}, false, false
			}
			return e.connector, e.available, e.known
		},
		Endpoints: e.endpoints,
		Inbox:     e.inbox,
		Tenants:   e.tenants,
		Config:    e.config,
		Decrypt:   func(b []byte) ([]byte, error) { return []byte("decrypted:" + string(b)), nil },
		Verifier:  e.verifier,
		Enqueuer:  e.enqueuer,
		Redis:     e.redis,
	})
	return e
}

func (e *env) post(module, token, contentType, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/_webhooks/"+module+"/"+token, strings.NewReader(body))
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	req.Header.Set("X-Signature", "sig")
	req = req.WithContext(route.WithParams(req.Context(), map[string]string{"module_name": module, "token": token}))
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	return rec
}

func (e *env) deliver(body string) *httptest.ResponseRecorder {
	return e.post(testModule, testToken, "application/json", body)
}

func TestAcceptedDeliveryIsStoredAcknowledgedAndEnqueued(t *testing.T) {
	e := newEnv()

	rec := e.deliver(`{"data":{"reference":"ref_1"}}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body)
	}
	if len(e.inbox.accepts) != 1 {
		t.Fatalf("accepts = %+v, want one stored delivery", e.inbox.accepts)
	}
	got := e.inbox.accepts[0]
	if got.tenantID != testTenantID || got.module != testModule || got.eventID != "evt_1" || string(got.payload) != `{"data":{"reference":"ref_1"}}` {
		t.Errorf("stored = %+v, want evt_1 for the tenant and module with the JSON body", got)
	}
	if len(e.enqueuer.jobs) != 1 {
		t.Fatalf("jobs = %+v, want one", e.enqueuer.jobs)
	}
	job := e.enqueuer.jobs[0]
	if job.tenantID != testTenantID || job.module != testModule || job.jobType != "paystack_process_inbox" {
		t.Errorf("job = %+v, want paystack_process_inbox for the tenant and module", job)
	}
	var payload struct {
		TenantID string `msgpack:"tenant_id"`
		InboxID  string `msgpack:"inbox_id"`
	}
	if err := msgpack.Unmarshal(job.payload, &payload); err != nil || payload.TenantID != testTenantID || payload.InboxID != "inbox-evt_1" {
		t.Errorf("job payload = %+v (%v), want {tenant_id, inbox_id}", payload, err)
	}
}

func TestDuplicateDeliveryIsAcknowledgedWithoutEnqueueing(t *testing.T) {
	e := newEnv()
	e.deliver(`{}`)

	rec := e.deliver(`{}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("redelivery status = %d, want 200", rec.Code)
	}
	if len(e.inbox.accepts) != 1 || len(e.enqueuer.jobs) != 1 {
		t.Errorf("after a redelivery: %d stored, %d enqueued; want 1 and 1", len(e.inbox.accepts), len(e.enqueuer.jobs))
	}
}

func TestPreChecksRunBeforeTenantResolution(t *testing.T) {
	tests := []struct {
		name       string
		module     string
		token      string
		mediaType  string
		redisAllow bool
		want       int
	}{
		{"unknown module", "connector_other", testToken, "application/json", true, http.StatusNotFound},
		{"malformed token", testModule, "short", "application/json", true, http.StatusNotFound},
		{"token with a symbol", testModule, strings.Repeat("a", 42) + "-", "application/json", true, http.StatusNotFound},
		{"wrong content type", testModule, testToken, "text/plain", true, http.StatusUnsupportedMediaType},
		{"missing content type", testModule, testToken, "", true, http.StatusUnsupportedMediaType},
		{"over the rate limit", testModule, testToken, "application/json", false, http.StatusTooManyRequests},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv()
			e.redis.allow = tt.redisAllow

			rec := e.post(tt.module, tt.token, tt.mediaType, `{}`)

			if rec.Code != tt.want {
				t.Errorf("status = %d, want %d", rec.Code, tt.want)
			}
			if e.endpoints.calls != 0 || e.verifier.calls != 0 {
				t.Errorf("tenant resolution ran %d times and the verifier %d times; want neither", e.endpoints.calls, e.verifier.calls)
			}
		})
	}
}

func TestMalformedTokenAndWrongContentTypeTouchNoRedisKey(t *testing.T) {
	e := newEnv()

	e.post(testModule, "short", "application/json", `{}`)
	e.post(testModule, testToken, "text/plain", `{}`)

	if len(e.redis.windowKeys) != 0 {
		t.Errorf("rate limit keys %v were created before the cheap checks passed", e.redis.windowKeys)
	}
}

func TestRateLimitIsKeyedByTokenAndAnswersRetryAfter(t *testing.T) {
	e := newEnv()
	e.redis.allow = false
	e.redis.retryAfter = 1500 * time.Millisecond

	rec := e.deliver(`{}`)

	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") != "2" {
		t.Errorf("status %d Retry-After %q, want 429 and 2", rec.Code, rec.Header().Get("Retry-After"))
	}
	if want := "webhook:rate:" + testModule + ":" + testToken; len(e.redis.windowKeys) != 1 || e.redis.windowKeys[0] != want {
		t.Errorf("rate limit keys = %v, want [%s]", e.redis.windowKeys, want)
	}
}

func TestRedisFailureFailsOpen(t *testing.T) {
	e := newEnv()
	e.redis.err = errors.New("redis down")

	if rec := e.deliver(`{}`); rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 when the limiter errors", rec.Code)
	}

	e = newEnv()
	e.h.Redis = nil
	if rec := e.deliver(`{}`); rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 without a Redis client", rec.Code)
	}
}

func TestContentTypeParametersAndCaseAreIgnored(t *testing.T) {
	e := newEnv()

	if rec := e.post(testModule, testToken, "Application/JSON; charset=utf-8", `{}`); rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 for a charset parameter", rec.Code)
	}
}

func TestUnknownRevokedAndWrongModuleTokensAre404(t *testing.T) {
	e := newEnv()
	e.endpoints.err = connectoringress.ErrEndpointNotFound

	rec := e.deliver(`{}`)

	if rec.Code != http.StatusNotFound || e.verifier.calls != 0 {
		t.Errorf("status %d with %d verifier calls, want 404 and none", rec.Code, e.verifier.calls)
	}
	if body := rec.Body.String(); !strings.Contains(body, "route_not_found") {
		t.Errorf("body = %s, want the same route_not_found error an unknown route gives", body)
	}
}

func TestTenantStateGatesDeliveries(t *testing.T) {
	tests := []struct {
		name   string
		tenant *tenant.Tenant
		err    error
		want   int
	}{
		{"active", &tenant.Tenant{Status: tenant.StatusActive}, nil, http.StatusOK},
		{"suspended tenants still get deliveries stored", &tenant.Tenant{Status: tenant.StatusSuspended}, nil, http.StatusOK},
		{"offboarding", &tenant.Tenant{Status: tenant.StatusOffboarding}, nil, http.StatusNotFound},
		{"deleted", &tenant.Tenant{Status: tenant.StatusDeleted}, nil, http.StatusNotFound},
		{"missing", nil, tenant.ErrTenantNotFound, http.StatusNotFound},
		{"lookup failure", nil, errors.New("db down"), http.StatusInternalServerError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv()
			e.tenants.tenant, e.tenants.err = tt.tenant, tt.err

			if rec := e.deliver(`{}`); rec.Code != tt.want {
				t.Errorf("status = %d, want %d", rec.Code, tt.want)
			}
		})
	}
}

func TestBodyOverOneMegabyteIsRejectedBeforeVerification(t *testing.T) {
	e := newEnv()

	rec := e.deliver(`{"pad":"` + strings.Repeat("x", MaxBodyBytes) + `"}`)

	if rec.Code != http.StatusRequestEntityTooLarge || e.verifier.calls != 0 {
		t.Errorf("status %d with %d verifier calls, want 413 and none", rec.Code, e.verifier.calls)
	}

	e = newEnv()
	if rec := e.deliver(`{"pad":"` + strings.Repeat("x", MaxBodyBytes-64) + `"}`); rec.Code != http.StatusOK {
		t.Errorf("a body just under the limit: status = %d, want 200", rec.Code)
	}
}

func TestVerifierReceivesSecretsHeadersAndRawBody(t *testing.T) {
	e := newEnv()
	e.config.values[testModule+".webhook_secret_previous"] = "previous"
	body := `{"data":{"reference":"ref_1"}}`

	e.deliver(body)

	got := e.verifier.got
	if len(got.Secrets) != 2 || string(got.Secrets[0]) != "current" || string(got.Secrets[1]) != "previous" {
		t.Errorf("secrets = %q, want current then previous", got.Secrets)
	}
	if string(got.Body) != body || got.Headers["X-Signature"][0] != "sig" {
		t.Errorf("body %q headers %v, want the raw body and the request headers", got.Body, got.Headers)
	}
}

func TestSecretsAreDecryptedWhenEncrypted(t *testing.T) {
	e := newEnv()
	e.config.encrypted[testModule+".webhook_secret"] = true

	e.deliver(`{}`)

	if got := e.verifier.got.Secrets; len(got) != 1 || string(got[0]) != "decrypted:current" {
		t.Errorf("secrets = %q, want the decrypted value", got)
	}
}

func TestUnsetSecretsAreOmitted(t *testing.T) {
	e := newEnv()
	e.config.values[testModule+".webhook_secret"] = ""

	e.deliver(`{}`)

	if got := e.verifier.got.Secrets; len(got) != 0 {
		t.Errorf("secrets = %q, want none", got)
	}
}

func TestInvalidSignatureIs401CountedAndStoresNothing(t *testing.T) {
	e := newEnv()
	e.verifier.verdict = wasm.WebhookVerdict{Outcome: wasm.WebhookRejected}

	for range 3 {
		if rec := e.deliver(`{}`); rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", rec.Code)
		}
	}

	if len(e.inbox.accepts) != 0 || len(e.enqueuer.jobs) != 0 {
		t.Errorf("an invalid delivery stored %d rows and enqueued %d jobs", len(e.inbox.accepts), len(e.enqueuer.jobs))
	}
	if want := "webhook:invalid:" + testModule + ":" + testToken; len(e.redis.incrKeys) != 3 || e.redis.incrKeys[0] != want {
		t.Errorf("failure counter keys = %v, want three increments of %s", e.redis.incrKeys, want)
	}
}

func TestVerifierOutcomesMapToStatuses(t *testing.T) {
	tests := []struct {
		name    string
		verdict wasm.WebhookVerdict
		want    int
	}{
		{"verifier error", wasm.WebhookVerdict{Outcome: wasm.WebhookVerifierError, Message: "boom"}, http.StatusInternalServerError},
		{"no verifier", wasm.WebhookVerdict{Outcome: wasm.WebhookNoVerifier}, http.StatusServiceUnavailable},
		{"unknown outcome", wasm.WebhookVerdict{}, http.StatusInternalServerError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv()
			e.verifier.verdict = tt.verdict

			rec := e.deliver(`{}`)

			if rec.Code != tt.want || len(e.inbox.accepts) != 0 {
				t.Errorf("status %d with %d stored, want %d and none", rec.Code, len(e.inbox.accepts), tt.want)
			}
		})
	}
}

func TestConnectorThatCannotVerifyIs503WithoutCallingTheVerifier(t *testing.T) {
	e := newEnv()
	e.connector.HasVerifier = false
	if rec := e.deliver(`{}`); rec.Code != http.StatusServiceUnavailable || e.verifier.calls != 0 {
		t.Errorf("no verifier export: status %d, %d verifier calls; want 503 and none", rec.Code, e.verifier.calls)
	}

	e = newEnv()
	e.available = false
	if rec := e.deliver(`{}`); rec.Code != http.StatusServiceUnavailable || e.verifier.calls != 0 {
		t.Errorf("module still loading: status %d, %d verifier calls; want 503 and none", rec.Code, e.verifier.calls)
	}
}

func TestEnqueueFailureFailsTheDeliveryWithoutStoringIt(t *testing.T) {
	e := newEnv()
	e.enqueuer.err = errors.New("queue down")

	rec := e.deliver(`{}`)

	if rec.Code != http.StatusInternalServerError || len(e.inbox.accepts) != 0 {
		t.Errorf("status %d with %d stored, want 500 and none so the provider retries", rec.Code, len(e.inbox.accepts))
	}
}

func TestConnectorWithoutAnInboxJobIs503AndStoresNothing(t *testing.T) {
	e := newEnv()
	e.connector.InboxJob = nil

	rec := e.deliver(`{}`)

	if rec.Code != http.StatusServiceUnavailable || e.verifier.calls != 0 || len(e.inbox.accepts) != 0 {
		t.Errorf("status %d, %d verifier calls, %d stored; want 503, none and none so the provider keeps retrying", rec.Code, e.verifier.calls, len(e.inbox.accepts))
	}
}

func TestAuthenticBodyThatDoesNotParseIsStoredRaw(t *testing.T) {
	e := newEnv()

	rec := e.deliver(`{"truncated":`)

	if rec.Code != http.StatusOK || len(e.inbox.accepts) != 1 {
		t.Fatalf("status %d with %d stored, want 200 and one so a signed event is never lost", rec.Code, len(e.inbox.accepts))
	}
	if got := string(e.inbox.accepts[0].payload); got != `{"raw":"eyJ0cnVuY2F0ZWQiOg=="}` {
		t.Errorf("stored payload = %s, want the raw body base64-encoded", got)
	}
}

func TestProbingWithUnknownTokensIsThrottledPerAddress(t *testing.T) {
	e := newEnv()
	e.endpoints.err = connectoringress.ErrEndpointNotFound

	for i := range MaxUnknownTokensPerIP {
		if rec := e.deliver(`{}`); rec.Code != http.StatusNotFound {
			t.Fatalf("probe %d: status = %d, want 404", i, rec.Code)
		}
	}
	lookups := e.endpoints.calls

	rec := e.deliver(`{}`)

	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("status after %d unknown tokens = %d, want 429", MaxUnknownTokensPerIP, rec.Code)
	}
	if e.endpoints.calls != lookups {
		t.Error("a throttled sender still reached the endpoint lookup")
	}
	windowKeys := len(e.redis.windowKeys)
	e.deliver(`{}`)
	if len(e.redis.windowKeys) != windowKeys {
		t.Error("a throttled sender still created a rate limit key")
	}
}

func TestOnlyUnknownTokensCountTowardsProbing(t *testing.T) {
	e := newEnv()

	for range MaxUnknownTokensPerIP + 5 {
		if rec := e.deliver(`{}`); rec.Code != http.StatusOK && rec.Code != http.StatusTooManyRequests {
			t.Fatalf("status = %d", rec.Code)
		}
	}

	if got := e.redis.counts["webhook:unknown:192.0.2.1:1234"]; got != 0 {
		t.Errorf("unknown-token count = %d after valid deliveries, want 0", got)
	}
}

func TestStorageFailureIs500(t *testing.T) {
	e := newEnv()
	e.inbox.err = errors.New("db down")

	if rec := e.deliver(`{}`); rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
}

func TestPayloadJSON(t *testing.T) {
	tests := []struct {
		name      string
		mediaType string
		body      string
		want      string
		wantErr   bool
	}{
		{"json as is", "application/json", `{"a":1}`, `{"a":1}`, false},
		{"json with a duplicate key", "application/json", `{"a":1,"a":2}`, `{"a":1,"a":2}`, false},
		{"invalid json", "application/json", `{"a":`, "", true},
		{"vendor json", "application/vnd.api+json", `[1]`, `[1]`, false},
		{"form fields", "application/x-www-form-urlencoded", `id=1&to=%2B233`, `{"id":"1","to":"+233"}`, false},
		{"form repeated field", "application/x-www-form-urlencoded", `a=1&a=2`, `{"a":["1","2"]}`, false},
		{"form with a bad escape", "application/x-www-form-urlencoded", `a=%zz`, "", true},
		{"other type is wrapped", "application/xml", `<a/>`, `{"raw":"PGEvPg=="}`, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := payloadJSON(tt.mediaType, []byte(tt.body))
			if (err != nil) != tt.wantErr {
				t.Fatalf("payloadJSON() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr && string(got) != tt.want {
				t.Errorf("payloadJSON() = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestFormConnectorStoresFormFieldsAsJSON(t *testing.T) {
	e := newEnv()
	e.connector.MediaType = "application/x-www-form-urlencoded"

	rec := e.post(testModule, testToken, "application/x-www-form-urlencoded", "id=7&status=Success")

	if rec.Code != http.StatusOK || len(e.inbox.accepts) != 1 {
		t.Fatalf("status %d accepts %+v, want one stored delivery", rec.Code, e.inbox.accepts)
	}
	if got := string(e.inbox.accepts[0].payload); got != `{"id":"7","status":"Success"}` {
		t.Errorf("stored payload = %s, want the form as a JSON object", got)
	}
}

func TestConnectorOf(t *testing.T) {
	jobs := []manifest.JobType{{Name: "send"}, {Name: "paystack_process_inbox"}}
	mod := func(typ string, status module.ModuleStatus, mutate func(*module.LoadedModule)) *module.LoadedModule {
		m := &module.LoadedModule{Manifest: manifest.Manifest{Name: "connector_paystack", Type: typ, JobTypes: jobs}, Status: status}
		if mutate != nil {
			mutate(m)
		}
		return m
	}

	tests := []struct {
		name          string
		m             *module.LoadedModule
		wantOK        bool
		wantAvailable bool
	}{
		{"nil module", nil, false, false},
		{"not a connector", mod("domain", module.StatusReady, nil), false, false},
		{"failed", mod("connector", module.StatusFailed, nil), false, false},
		{"unloaded", mod("connector", module.StatusUnloaded, nil), false, false},
		{"ready", mod("connector", module.StatusReady, nil), true, true},
		{"disabled still receives", mod("connector", module.StatusDisabled, nil), true, true},
		{"draining during a reload", mod("connector", module.StatusDraining, nil), true, true},
		{"still warming", mod("connector", module.StatusWarming, nil), true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, available, ok := ConnectorOf(tt.m)
			if ok != tt.wantOK || available != tt.wantAvailable {
				t.Errorf("ConnectorOf() ok=%v available=%v, want ok=%v available=%v", ok, available, tt.wantOK, tt.wantAvailable)
			}
		})
	}

	c, _, _ := ConnectorOf(mod("connector", module.StatusReady, func(m *module.LoadedModule) {
		m.HasWebhookVerifier = true
		m.Manifest.WebhookContentType = "application/x-www-form-urlencoded"
	}))
	if c.Name != "connector_paystack" || c.MediaType != "application/x-www-form-urlencoded" || !c.HasVerifier {
		t.Errorf("connector = %+v, want the name, declared media type and verifier flag", c)
	}
	if c.InboxJob == nil || c.InboxJob.Name != "paystack_process_inbox" {
		t.Errorf("InboxJob = %+v, want paystack_process_inbox (module name minus connector_, plus _process_inbox)", c.InboxJob)
	}
	if def, _, _ := ConnectorOf(mod("connector", module.StatusReady, nil)); def.MediaType != "application/json" || def.HasVerifier {
		t.Errorf("defaults = %+v, want application/json and no verifier", def)
	}
	if none, _, _ := ConnectorOf(&module.LoadedModule{Manifest: manifest.Manifest{Name: "connector_x", Type: "connector"}, Status: module.StatusReady}); none.InboxJob != nil {
		t.Errorf("InboxJob = %+v, want nil for a connector declaring no inbox job", none.InboxJob)
	}
}
