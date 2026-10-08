// Package webhookingress serves the engine-owned inbound webhook route,
// POST /_webhooks/{module_name}/{token} (connector-guide.md §3): it resolves
// the tenant from the URL token, captures the bounded raw body, reads the
// tenant's signing secrets, runs the connector's registered verifier, records
// the accepted delivery idempotently and enqueues the connector's processing
// job. The route needs no tenant or auth middleware: the token is the only
// identity the sender has.
package webhookingress

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"maps"
	"math"
	"mime"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/connectoringress"
	"github.com/djangbahevans/goerp/internal/engine/httperr"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/route"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	"github.com/djangbahevans/goerp/internal/engine/wasm"
	"github.com/rs/zerolog/log"
	"github.com/vmihailenco/msgpack/v5"
)

const (
	// MaxBodyBytes bounds the captured request body.
	MaxBodyBytes = 1 << 20

	// RateLimit is the requests per RateWindow one token may make.
	RateLimit  = 120
	RateWindow = time.Minute

	// MaxUnknownTokensPerIP is how many requests per minute from one address
	// may name a token that resolves to no endpoint before that address is
	// answered 429 without any lookup. A genuine provider never names an unknown
	// token, so this bounds probing without limiting providers by IP.
	MaxUnknownTokensPerIP = 30

	// failureDetailThreshold is how many invalid-signature failures per token
	// per minute are logged in detail before logging is sampled.
	failureDetailThreshold = 10
	failureSampleEvery     = 100

	// Webhook secret config keys (connector-guide.md §4).
	secretKey         = "webhook_secret"
	previousSecretKey = "webhook_secret_previous"

	// inboxJobSuffix names a connector's processing job: its module name
	// without the connector_ prefix, then this suffix (paystack_process_inbox
	// for connector_paystack).
	inboxJobSuffix = "_process_inbox"

	tokenLength = 43
)

// Connector is what the pipeline needs to know about a connector module.
type Connector struct {
	Name        string
	MediaType   string
	HasVerifier bool
	// InboxJob is the connector's processing job type, nil when it declares none.
	InboxJob *manifest.JobType
	Source   wasm.WebhookVerifierSource
}

// ConnectorOf describes m for the pipeline. ok is false when m is not a
// connector that can receive a delivery: it is not type "connector", or it
// failed to load or was unloaded.
func ConnectorOf(m *module.LoadedModule) (c Connector, available, ok bool) {
	if m == nil || m.Manifest.Type != "connector" {
		return Connector{}, false, false
	}
	switch m.Status {
	case module.StatusFailed, module.StatusUnloaded:
		return Connector{}, false, false
	}

	name := m.Manifest.Name
	c = Connector{
		Name:        name,
		MediaType:   m.Manifest.WebhookMediaType(),
		HasVerifier: m.HasWebhookVerifier,
		Source:      wasm.WebhookVerifierSource{ModuleName: name, Pool: m.Pool, Compiled: m.CompiledModule},
	}
	wantJob := strings.TrimPrefix(name, "connector_") + inboxJobSuffix
	if i := slices.IndexFunc(m.Manifest.JobTypes, func(jt manifest.JobType) bool { return jt.Name == wantJob }); i >= 0 {
		c.InboxJob = &m.Manifest.JobTypes[i]
	}

	// A module still loading cannot verify yet; the sender should retry.
	available = m.Status == module.StatusReady || m.Status == module.StatusDisabled || m.Status == module.StatusDraining
	return c, available, true
}

// Endpoints resolves a URL token to its tenant.
type Endpoints interface {
	ResolveEndpoint(ctx context.Context, token, moduleName string) (tenantID string, err error)
}

// Inbox records accepted deliveries.
type Inbox interface {
	AcceptDelivery(ctx context.Context, tenantID, moduleName, providerEventID string, payload []byte, enqueue func(ctx context.Context, tx *sql.Tx, inboxID string) error) (id string, inserted bool, err error)
}

// Tenants looks a tenant up by ID.
type Tenants interface {
	GetByID(ctx context.Context, id string) (*tenant.Tenant, error)
}

// Config reads a tenant's fully qualified "{module}.{key}" config value.
type Config interface {
	Get(ctx context.Context, tenantID, key string) (value string, encrypted, found bool, err error)
}

// Verifier runs a connector's webhook verifier.
type Verifier interface {
	VerifyWebhook(ctx context.Context, src wasm.WebhookVerifierSource, req abiv1.WebhookVerifyRequest) wasm.WebhookVerdict
}

// Enqueuer inserts a connector's job on the delivery's transaction.
type Enqueuer interface {
	EnqueueModuleJobTx(ctx context.Context, tx *sql.Tx, tenantID, moduleName string, jt manifest.JobType, payload []byte) error
}

// Redis is the subset of the Redis client the pipeline uses.
type Redis interface {
	SlidingWindowAllow(ctx context.Context, key string, limit int, window time.Duration) (allowed bool, retryAfter time.Duration, err error)
	IncrWithTTL(ctx context.Context, key string, ttl time.Duration) (count int64, err error)
	Get(ctx context.Context, key string) (value string, found bool, err error)
}

// Deps are the collaborators of Handler. Redis may be nil, in which case
// rate limiting and failure counting are skipped, as the engine's other
// limiter does when Redis is unavailable.
type Deps struct {
	Connector func(moduleName string) (Connector, bool, bool)
	Endpoints Endpoints
	Inbox     Inbox
	Tenants   Tenants
	Config    Config
	// Decrypt opens an encrypted config value.
	Decrypt  func(ciphertext []byte) ([]byte, error)
	Verifier Verifier
	Enqueuer Enqueuer
	Redis    Redis
}

// Handler serves POST /_webhooks/{module_name}/{token}.
type Handler struct {
	Deps
}

func NewHandler(deps Deps) *Handler {
	return &Handler{Deps: deps}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	params := route.ParamsFromContext(ctx)
	moduleName, token := params["module_name"], params["token"]

	connector, available, ok := h.Connector(moduleName)
	if !ok || !validToken(token) {
		notFound(ctx, w)
		return
	}
	if !mediaTypeMatches(r.Header.Get("Content-Type"), connector.MediaType) {
		httperr.Write(ctx, w, http.StatusUnsupportedMediaType, "unsupported_media_type", "Content-Type must be "+connector.MediaType)
		return
	}
	if h.probing(ctx, r) {
		httperr.Write(ctx, w, http.StatusTooManyRequests, "rate_limit_exceeded", "too many requests")
		return
	}
	if !h.allowRequest(ctx, w, moduleName, token) {
		return
	}

	tenantID, err := h.Endpoints.ResolveEndpoint(ctx, token, moduleName)
	if errors.Is(err, connectoringress.ErrEndpointNotFound) {
		h.recordUnknownToken(ctx, r)
		notFound(ctx, w)
		return
	}
	if err != nil {
		internalError(ctx, w, "resolve webhook endpoint", err)
		return
	}
	if !h.tenantAcceptsDeliveries(ctx, w, tenantID) {
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, MaxBodyBytes))
	if err != nil {
		if _, tooLarge := errors.AsType[*http.MaxBytesError](err); tooLarge {
			httperr.Write(ctx, w, http.StatusRequestEntityTooLarge, "payload_too_large", fmt.Sprintf("body exceeds %d bytes", MaxBodyBytes))
			return
		}
		httperr.Write(ctx, w, http.StatusBadRequest, "invalid_body", "could not read the request body")
		return
	}

	if !available || !connector.HasVerifier || connector.InboxJob == nil {
		log.Error().Str("module", moduleName).Bool("available", available).Bool("has_verifier", connector.HasVerifier).
			Bool("has_inbox_job", connector.InboxJob != nil).Msg("webhook ingress: connector cannot receive deliveries")
		httperr.Write(ctx, w, http.StatusServiceUnavailable, "webhook_unavailable", "this webhook endpoint is not ready to receive deliveries")
		return
	}

	secrets, err := h.secrets(ctx, tenantID, moduleName)
	if err != nil {
		internalError(ctx, w, "read webhook secrets", err)
		return
	}

	verdict := h.Verifier.VerifyWebhook(ctx, connector.Source, abiv1.WebhookVerifyRequest{Secrets: secrets, Headers: r.Header, Body: body})
	switch verdict.Outcome {
	case wasm.WebhookRejected:
		h.recordInvalidSignature(ctx, moduleName, token, r)
		httperr.Write(ctx, w, http.StatusUnauthorized, "invalid_signature", "the webhook signature is not valid")
		return
	case wasm.WebhookNoVerifier:
		log.Error().Str("module", moduleName).Msg("webhook ingress: connector exports no webhook verifier")
		httperr.Write(ctx, w, http.StatusServiceUnavailable, "webhook_unavailable", "this webhook endpoint is not ready to receive deliveries")
		return
	case wasm.WebhookVerifierError:
		internalError(ctx, w, "run webhook verifier", errors.New(verdict.Message))
		return
	case wasm.WebhookAccepted:
	default:
		internalError(ctx, w, "run webhook verifier", fmt.Errorf("unexpected verifier outcome %d", verdict.Outcome))
		return
	}

	payload, err := payloadJSON(connector.MediaType, body)
	if err != nil {
		log.Warn().Err(err).Str("module", moduleName).Msg("webhook ingress: authentic body does not parse as " + connector.MediaType + "; storing it raw")
		payload = rawPayload(body)
	}

	_, _, err = h.Inbox.AcceptDelivery(ctx, tenantID, moduleName, verdict.ProviderEventID, payload,
		func(ctx context.Context, tx *sql.Tx, inboxID string) error {
			return h.enqueue(ctx, tx, tenantID, connector, inboxID)
		})
	if err != nil {
		internalError(ctx, w, "accept webhook delivery", err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"accepted"}`))
}

func notFound(ctx context.Context, w http.ResponseWriter) {
	httperr.Write(ctx, w, http.StatusNotFound, "route_not_found", "No route matches this path")
}

func internalError(ctx context.Context, w http.ResponseWriter, operation string, err error) {
	log.Error().Err(err).Msg("webhook ingress: " + operation)
	httperr.Write(ctx, w, http.StatusInternalServerError, "internal_error", "the delivery could not be processed")
}

// validToken reports whether token has the shape connectoringress mints, so a
// malformed one never reaches Redis or Postgres.
func validToken(token string) bool {
	if len(token) != tokenLength {
		return false
	}
	for _, c := range token {
		isBase62 := c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
		if !isBase62 {
			return false
		}
	}
	return true
}

// mediaTypeMatches compares the request's Content-Type with want as a media
// type, ignoring case and parameters such as charset.
func mediaTypeMatches(contentType, want string) bool {
	got, _, err := mime.ParseMediaType(contentType)
	return err == nil && got == want
}

// allowRequest applies the per-token rate limit. It keys on the URL token, not
// the sender's IP: providers call from many addresses.
func (h *Handler) allowRequest(ctx context.Context, w http.ResponseWriter, moduleName, token string) bool {
	if h.Redis == nil {
		return true
	}
	key := "webhook:rate:" + moduleName + ":" + token
	allowed, retryAfter, err := h.Redis.SlidingWindowAllow(ctx, key, RateLimit, RateWindow)
	if err != nil {
		log.Warn().Err(err).Msg("webhook ingress: rate limit check failed, failing open")
		return true
	}
	if !allowed {
		w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(retryAfter.Seconds()))))
		httperr.Write(ctx, w, http.StatusTooManyRequests, "rate_limit_exceeded", "too many requests")
		return false
	}
	return true
}

func unknownTokenKey(r *http.Request) string {
	return "webhook:unknown:" + r.RemoteAddr
}

// probing reports whether the sender has named too many unknown tokens
// recently, checked before any token is looked up.
func (h *Handler) probing(ctx context.Context, r *http.Request) bool {
	if h.Redis == nil {
		return false
	}
	value, found, err := h.Redis.Get(ctx, unknownTokenKey(r))
	if err != nil || !found {
		return false
	}
	count, err := strconv.ParseInt(value, 10, 64)
	return err == nil && count >= MaxUnknownTokensPerIP
}

func (h *Handler) recordUnknownToken(ctx context.Context, r *http.Request) {
	if h.Redis == nil {
		return
	}
	if _, err := h.Redis.IncrWithTTL(ctx, unknownTokenKey(r), time.Minute); err != nil {
		log.Warn().Err(err).Msg("webhook ingress: could not count unknown token")
	}
}

// tenantAcceptsDeliveries answers 404 for a tenant that is gone or being
// offboarded; a suspended tenant's deliveries are still stored.
func (h *Handler) tenantAcceptsDeliveries(ctx context.Context, w http.ResponseWriter, tenantID string) bool {
	t, err := h.Tenants.GetByID(ctx, tenantID)
	if errors.Is(err, tenant.ErrTenantNotFound) {
		notFound(ctx, w)
		return false
	}
	if err != nil {
		internalError(ctx, w, "look up tenant", err)
		return false
	}
	if t.Status == tenant.StatusDeleted || t.Status == tenant.StatusOffboarding {
		notFound(ctx, w)
		return false
	}
	return true
}

// secrets returns the connector's acceptable signing secrets, current first.
func (h *Handler) secrets(ctx context.Context, tenantID, moduleName string) ([][]byte, error) {
	var secrets [][]byte
	for _, key := range []string{secretKey, previousSecretKey} {
		value, encrypted, found, err := h.Config.Get(ctx, tenantID, moduleName+"."+key)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", key, err)
		}
		if !found || value == "" {
			continue
		}
		raw := []byte(value)
		if encrypted {
			raw, err = h.Decrypt(raw)
			if err != nil {
				return nil, fmt.Errorf("decrypt %s: %w", key, err)
			}
		}
		secrets = append(secrets, raw)
	}
	return secrets, nil
}

// recordInvalidSignature counts every failure per token, and logs the first few
// in detail then one in failureSampleEvery, so a flood of bad requests against
// one token cannot become a flood of log lines.
func (h *Handler) recordInvalidSignature(ctx context.Context, moduleName, token string, r *http.Request) {
	count := int64(1)
	if h.Redis != nil {
		var err error
		count, err = h.Redis.IncrWithTTL(ctx, "webhook:invalid:"+moduleName+":"+token, time.Minute)
		if err != nil {
			log.Warn().Err(err).Msg("webhook ingress: could not count invalid signature")
		}
	}
	if count > failureDetailThreshold && count%failureSampleEvery != 0 {
		return
	}

	headers := slices.Sorted(maps.Keys(r.Header))
	log.Warn().Str("module", moduleName).Int64("failures_this_minute", count).
		Strs("header_names", headers).Int64("content_length", r.ContentLength).
		Msg("webhook ingress: invalid signature")
}

// enqueue schedules the connector's processing job for a new inbox row.
func (h *Handler) enqueue(ctx context.Context, tx *sql.Tx, tenantID string, c Connector, inboxID string) error {
	payload, err := msgpack.Marshal(struct {
		TenantID string `msgpack:"tenant_id"`
		InboxID  string `msgpack:"inbox_id"`
	}{tenantID, inboxID})
	if err != nil {
		return fmt.Errorf("encode job payload: %w", err)
	}
	return h.Enqueuer.EnqueueModuleJobTx(ctx, tx, tenantID, c.Name, *c.InboxJob, payload)
}

// payloadJSON converts an accepted body into the JSON document stored in the
// inbox: a JSON body as is, a form body as an object of its fields, and any
// other body base64-encoded under "raw". It errors when a JSON or form body
// does not parse.
func payloadJSON(mediaType string, body []byte) ([]byte, error) {
	switch {
	case mediaType == "application/json" || strings.HasSuffix(mediaType, "+json"):
		if !jsontext.Value(body).IsValid(jsontext.AllowDuplicateNames(true)) {
			return nil, errors.New("invalid JSON")
		}
		return body, nil
	case mediaType == "application/x-www-form-urlencoded":
		values, err := url.ParseQuery(string(body))
		if err != nil {
			return nil, err
		}
		fields := make(map[string]any, len(values))
		for name, vs := range values {
			if len(vs) == 1 {
				fields[name] = vs[0]
			} else {
				fields[name] = vs
			}
		}
		return json.Marshal(fields, json.Deterministic(true))
	}
	return rawPayload(body), nil
}

// rawPayload wraps a body that cannot be represented as JSON, so an authentic
// delivery is stored rather than lost; the connector's job decides what to do
// with it.
func rawPayload(body []byte) []byte {
	payload, _ := json.Marshal(map[string]string{"raw": base64.StdEncoding.EncodeToString(body)})
	return payload
}
