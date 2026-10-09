// Package config loads and validates shared engine environment settings.
// Backend-specific settings are parsed only by the selected backend.
package config

import (
	"fmt"
	"net"
	"time"

	"github.com/caarlos0/env/v11"
	"github.com/djangbahevans/goerp/internal/engine/l10n"
	"github.com/go-playground/validator/v10"
)

type Environment string

const (
	Development Environment = "development"
	Staging     Environment = "staging"
	Production  Environment = "production"
)

type Config struct {
	ListenAddr              string        `env:"GOERP_LISTEN_ADDR" envDefault:":8080" validate:"hostname_port"`
	ServerReadTimeout       time.Duration `env:"GOERP_SERVER_READ_TIMEOUT" envDefault:"30s"`
	ServerReadHeaderTimeout time.Duration `env:"GOERP_SERVER_READ_HEADER_TIMEOUT" envDefault:"5s"`
	ServerWriteTimeout      time.Duration `env:"GOERP_SERVER_WRITE_TIMEOUT" envDefault:"60s"`
	ServerIdleTimeout       time.Duration `env:"GOERP_SERVER_IDLE_TIMEOUT" envDefault:"120s"`
	ServerMaxHeaderBytes    int           `env:"GOERP_SERVER_MAX_HEADER_BYTES" envDefault:"1048576"`
	TLSCertFile             string        `env:"GOERP_TLS_CERT_FILE"`
	TLSKeyFile              string        `env:"GOERP_TLS_KEY_FILE"`
	// TrustedProxies is the set of IPs/CIDRs realIPMiddleware trusts to
	// report a client's real address via X-Forwarded-For/X-Real-IP —
	// engine-internals.md §6 step 3. An untrusted peer's own
	// X-Forwarded-For is never honored, so an empty list (the default)
	// means every request's real IP is simply its raw RemoteAddr.
	TrustedProxies []string `env:"GOERP_TRUSTED_PROXIES"`
	// RateLimitMax/RateLimitWindow are the engine-wide default rate limit
	// rateLimitMiddleware applies to any route that doesn't declare its
	// own stricter RouteManifest.RateLimit.
	RateLimitMax       int           `env:"GOERP_RATE_LIMIT_MAX" envDefault:"600"`
	RateLimitWindow    time.Duration `env:"GOERP_RATE_LIMIT_WINDOW" envDefault:"60s"`
	AdminAddr          string        `env:"GOERP_ADMIN_ADDR" envDefault:"127.0.0.1:8081" validate:"loopback"`
	AdminMaxBodyBytes  int64         `env:"GOERP_ADMIN_MAX_BODY_BYTES" envDefault:"10485760"`
	AdminMaxConcurrent int           `env:"GOERP_ADMIN_MAX_CONCURRENT" envDefault:"20"`
	Environment        string        `env:"GOERP_ENV" envDefault:"production" validate:"oneof=production staging development"`
	ModuleDev          bool          `env:"GOERP_MODULE_DEV" envDefault:"false"`
	LogLevel           string        `env:"GOERP_LOG_LEVEL" envDefault:"info" validate:"oneof=debug info warn error"`
	LogFormat          string        `env:"GOERP_LOG_FORMAT" envDefault:"json" validate:"oneof=json text"`
	CompilationCache   string        `env:"GOERP_COMPILATION_CACHE" envDefault:"./wasm-cache"`
	ModuleDir          string        `env:"GOERP_MODULE_DIR" envDefault:"./modules"`
	ShellDir           string        `env:"GOERP_SHELL_DIR"`
	ShutdownTimeout    time.Duration `env:"GOERP_SHUTDOWN_TIMEOUT" envDefault:"30s"`
	ShutdownDrainDelay time.Duration `env:"GOERP_SHUTDOWN_DRAIN_DELAY" envDefault:"5s"`
	HotReloadEnabled   bool          `env:"GOERP_HOT_RELOAD_ENABLED" envDefault:"false"`
	HotReloadLockTTL   time.Duration `env:"GOERP_HOT_RELOAD_LOCK_TTL" envDefault:"60s"`

	// SyncSubscriberTimeout bounds each inline synchronous subscriber independently of the
	// emitter's request timeout.
	SyncSubscriberTimeout time.Duration `env:"GOERP_SYNC_SUBSCRIBER_TIMEOUT" envDefault:"3s"`

	// SyncProviderTimeout is host.jobs.dispatch_provider_sync's default
	// budget when the caller gives no timeout_ms (host-abi-reference.md
	// §10) — higher than SyncSubscriberTimeout, since the target handler
	// is expected to make its own outbound API call.
	SyncProviderTimeout time.Duration `env:"GOERP_SYNC_PROVIDER_TIMEOUT" envDefault:"15s"`

	// EventLedgerRetention is how long event_deliveries rows are kept; it
	// must exceed the longest span over which a duplicate delivery can arrive
	// (data-layer.md §2.6 "Event delivery ledger").
	EventLedgerRetention time.Duration `env:"GOERP_EVENT_LEDGER_RETENTION" envDefault:"840h" validate:"min=1h"`

	DBPrimaryDSN                string `env:"GOERP_DB_PRIMARY_DSN,required"`
	DBReplicaDSN                string `env:"GOERP_DB_REPLICA_DSN"`
	DBSchemaSyncDSN             string `env:"GOERP_DB_SCHEMA_SYNC_DSN,required,notEmpty"`
	DBMaxConcurrentTransactions int    `env:"GOERP_DB_MAX_CONCURRENT_TRANSACTIONS" envDefault:"100" validate:"min=1"`

	ORMBulkMaxRows      int           `env:"GOERP_ORM_BULK_MAX_ROWS" envDefault:"1000" validate:"min=1"`
	ORMStatementTimeout time.Duration `env:"GOERP_ORM_STATEMENT_TIMEOUT" envDefault:"30s"`

	RedisAddr           string   `env:"GOERP_REDIS_ADDR" envDefault:"localhost:6379" validate:"hostname_port"`
	RedisSentinelAddrs  []string `env:"GOERP_REDIS_SENTINEL_ADDRS" validate:"dive,hostname_port"`
	RedisSentinelMaster string   `env:"GOERP_REDIS_SENTINEL_MASTER" envDefault:"mymaster"`
	RedisDB             int      `env:"GOERP_REDIS_DB" envDefault:"0" validate:"min=0"`
	RedisMaxRetries     int      `env:"GOERP_REDIS_MAX_RETRIES" envDefault:"3" validate:"min=0"`

	MeilisearchURL    string `env:"GOERP_MEILISEARCH_URL" validate:"omitempty,http_url"`
	MeilisearchAPIKey string `env:"GOERP_MEILISEARCH_API_KEY"`

	SchemaSyncDDLStatementTimeout time.Duration `env:"GOERP_SCHEMA_SYNC_STATEMENT_TIMEOUT" envDefault:"30s"`
	SchemaSyncConcurrency         int           `env:"GOERP_SCHEMA_SYNC_CONCURRENCY" envDefault:"8"`
	QueueCriticalConcurrency      int           `env:"GOERP_QUEUE_CRITICAL_CONCURRENCY" envDefault:"5"`
	QueueDefaultConcurrency       int           `env:"GOERP_QUEUE_DEFAULT_CONCURRENCY" envDefault:"10"`
	QueueBulkConcurrency          int           `env:"GOERP_QUEUE_BULK_CONCURRENCY" envDefault:"3"`
	QueueSearchConcurrency        int           `env:"GOERP_QUEUE_SEARCH_CONCURRENCY" envDefault:"5"`
	QueueEmailConcurrency         int           `env:"GOERP_QUEUE_EMAIL_CONCURRENCY" envDefault:"5"`
	QueueAdminConcurrency         int           `env:"GOERP_QUEUE_ADMIN_CONCURRENCY" envDefault:"5"`
	QueueEventsConcurrency        int           `env:"GOERP_QUEUE_EVENTS_CONCURRENCY" envDefault:"10"`

	SecretsBackend string `env:"GOERP_SECRETS_BACKEND" envDefault:"env" validate:"oneof=env vault aws_secretsmanager"`
	EnableAPIKeys  bool   `env:"GOERP_ENABLE_API_KEYS" envDefault:"false"`
	// Argon2MemoryBudgetMB caps memory spent on concurrent Argon2id
	// operations: budget / 64 MB (per hash) slots, auth-internals.md §15
	// "Global Argon2 verification limit". Argon2AcquireTimeout is how long
	// a request waits for a slot before a 503.
	Argon2MemoryBudgetMB int           `env:"GOERP_ARGON2_MEMORY_BUDGET_MB" envDefault:"1024" validate:"min=64"`
	Argon2AcquireTimeout time.Duration `env:"GOERP_ARGON2_ACQUIRE_TIMEOUT" envDefault:"500ms"`

	// RegistrationEnabled gates POST /auth/register and GET
	// /auth/check-slug (404 when off). RequireEmailVerification is the
	// platform email-verification policy (auth-internals.md §3 "Email
	// verification policy").
	RegistrationEnabled      bool     `env:"GOERP_REGISTRATION_ENABLED" envDefault:"false"`
	ReservedSlugs            []string `env:"GOERP_RESERVED_SLUGS"`
	RequireEmailVerification string   `env:"GOERP_REQUIRE_EMAIL_VERIFICATION" envDefault:"tenant_choice" validate:"oneof=required tenant_choice off"`
	// AvailableLocales are the locales a user can choose on the Appearance
	// page when their tenant sets none of its own (l10n-guide.md §2).
	AvailableLocales []string `env:"GOERP_AVAILABLE_LOCALES" envDefault:"en,fr,ar" validate:"min=1"`
	TermsURL         string   `env:"GOERP_TERMS_URL" validate:"omitempty,http_url"`

	WebAuthnRPID          string   `env:"GOERP_WEBAUTHN_RP_ID"`
	WebAuthnRPDisplayName string   `env:"GOERP_WEBAUTHN_RP_DISPLAY_NAME" envDefault:"GoERP"`
	WebAuthnRPOrigins     []string `env:"GOERP_WEBAUTHN_RP_ORIGINS" envSeparator:","`

	StorageBackend      string   `env:"GOERP_STORAGE_BACKEND" envDefault:"local" validate:"oneof=local seaweedfs s3 r2 gcs"`
	StorageBucket       string   `env:"GOERP_STORAGE_BUCKET" envDefault:"goerp-files"`
	StorageMaxFileBytes int64    `env:"GOERP_STORAGE_MAX_FILE_BYTES" envDefault:"104857600"`
	StorageAllowedTypes []string `env:"GOERP_STORAGE_ALLOWED_TYPES"`
	StorageBlockedTypes []string `env:"GOERP_STORAGE_BLOCKED_TYPES" envDefault:"application/x-executable,application/x-msdownload,application/x-msdos-program,application/x-sh,application/x-bat,application/vnd.microsoft.portable-executable"`

	PoolWarmSize      int           `env:"GOERP_POOL_WARM_SIZE" envDefault:"4"`
	PoolMaxSize       int           `env:"GOERP_POOL_MAX_SIZE" envDefault:"16"`
	PoolBorrowTimeout time.Duration `env:"GOERP_POOL_BORROW_TIMEOUT" envDefault:"5s"`
	PoolMaxMemoryByes uint32        `env:"GOERP_POOL_MAX_MEMORY_BYTES" envDefault:"16777216"`

	SMTPHost string `env:"GOERP_SMTP_HOST" envDefault:"localhost"`
	SMTPPort int    `env:"GOERP_SMTP_PORT" envDefault:"1025"`
	SMTPUser string `env:"GOERP_SMTP_USER"`
	SMTPPass string `env:"GOERP_SMTP_PASSWORD"`
	SMTPFrom string `env:"GOERP_SMTP_FROM" envDefault:"noreply@goerp.local"`

	NotificationSMTPAllowPrivateHosts bool `env:"GOERP_NOTIFICATION_SMTP_ALLOW_PRIVATE_HOSTS" envDefault:"false"`

	// AppBaseURL is the app's URL on the shared-domain host. Emailed links
	// keep its scheme and port on the tenant's default domain,
	// {slug}.{PlatformDomain}. Its host is the one unresolved host
	// GET /auth/tenant-context treats as the shared domain.
	AppBaseURL string `env:"GOERP_APP_BASE_URL" envDefault:"http://localhost:8080" validate:"http_url"`

	// OTel uses the standard SDK environment names across languages.
	OTelExporterOTLPEndpoint string `env:"OTEL_EXPORTER_OTLP_ENDPOINT"`
	OTelServiceName          string `env:"OTEL_SERVICE_NAME" envDefault:"goerp-engine"`
	OTelInsecure             bool   `env:"OTEL_EXPORTER_OTLP_INSECURE" envDefault:"true"`

	// PlatformDomain is appended to a tenant's slug to build its default
	// subdomain at provisioning time (e.g. slug "acme" + PlatformDomain
	// "goerp.local" = "acme.goerp.local") — multitenancy-internals.md §6
	// step 8 "RegisterDomain".
	PlatformDomain string `env:"GOERP_PLATFORM_DOMAIN" envDefault:"goerp.local"`
}

func Load() (*Config, error) {
	validate := validator.New(validator.WithRequiredStructEnabled())
	if err := validate.RegisterValidation("loopback", isLoopback); err != nil {
		return nil, err
	}

	cfg := &Config{}

	if err := env.Parse(cfg); err != nil {
		return nil, err
	}

	if err := validate.Struct(cfg); err != nil {
		return cfg, err
	}
	if cfg.ModuleDev && (cfg.Environment != string(Development) || cfg.PlatformDomain != "localhost") {
		return cfg, fmt.Errorf("GOERP_MODULE_DEV requires GOERP_ENV=development and GOERP_PLATFORM_DOMAIN=localhost")
	}
	for _, locale := range cfg.AvailableLocales {
		if !l10n.ValidLocale(locale) {
			return cfg, fmt.Errorf("GOERP_AVAILABLE_LOCALES: %q is not a BCP 47 locale", locale)
		}
	}

	return cfg, nil
}

func isLoopback(fl validator.FieldLevel) bool {
	raw := fl.Field().Interface().(string)

	host, _, err := net.SplitHostPort(raw)
	if err != nil {
		host = raw
	}

	if host == "localhost" {
		return true
	}

	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
