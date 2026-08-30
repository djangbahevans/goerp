// Package config loads the engine's configuration from environment
// variables (caarlos0/env, matching engine-internals.md §14 — "All
// configuration is via environment variables") and validates it
// (go-playground/validator/v10). Fields specific to a single secrets or
// storage backend (Vault auth method, AWS region, etc.) deliberately don't
// live here — each backend package parses its own env vars only once that
// backend is actually selected, so Config only grows with what's genuinely
// engine-wide.
package config

import (
	"net"
	"time"

	"github.com/caarlos0/env/v11"
	"github.com/go-playground/validator/v10"
)

type Environment string

const (
	Development Environment = "development"
	Staging     Environment = "staging"
	Production  Environment = "production"
)

type Config struct {
	// Core
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
	LogLevel           string        `env:"GOERP_LOG_LEVEL" envDefault:"info" validate:"oneof=debug info warn error"`
	LogFormat          string        `env:"GOERP_LOG_FORMAT" envDefault:"json" validate:"oneof=json text"`
	CompilationCache   string        `env:"GOERP_COMPILATION_CACHE" envDefault:"./wasm-cache"`
	ModuleDir          string        `env:"GOERP_MODULE_DIR" envDefault:"./modules"`
	ShutdownTimeout    time.Duration `env:"GOERP_SHUTDOWN_TIMEOUT" envDefault:"30s"`
	ShutdownDrainDelay time.Duration `env:"GOERP_SHUTDOWN_DRAIN_DELAY" envDefault:"5s"`
	// HotReloadEnabled gates whether Engine.Start launches
	// internal/engine/hotreload's trigger goroutines (fsnotify, pub/sub,
	// registry poll) at all. Defaults to false: goerp#452 only builds the
	// trigger/coordination scaffolding — the leader and follower paths it
	// calls into (goerp#467, goerp#490) aren't implemented yet, so every
	// real reload attempt would currently just fail. hotreload.Coordinator
	// itself is always constructed and directly testable regardless of
	// this flag.
	HotReloadEnabled bool          `env:"GOERP_HOT_RELOAD_ENABLED" envDefault:"false"`
	HotReloadLockTTL time.Duration `env:"GOERP_HOT_RELOAD_LOCK_TTL" envDefault:"60s"`

	// SyncSubscriberTimeout bounds each async:false event subscriber's
	// execution during inline synchronous dispatch (events.WithSync(),
	// goerp#129, event-system.md §8 "Fan-out and timeout") — independent
	// of and nested inside the emitter's own request timeout.
	SyncSubscriberTimeout time.Duration `env:"GOERP_SYNC_SUBSCRIBER_TIMEOUT" envDefault:"3s"`

	// Database
	DBPrimaryDSN                string `env:"GOERP_DB_PRIMARY_DSN,required"`
	DBReplicaDSN                string `env:"GOERP_DB_REPLICA_DSN"`
	DBSchemaSyncDSN             string `env:"GOERP_DB_SCHEMA_SYNC_DSN"`
	DBMaxConcurrentTransactions int    `env:"GOERP_DB_MAX_CONCURRENT_TRANSACTIONS" envDefault:"100" validate:"min=1"`

	// Redis
	RedisAddr           string   `env:"GOERP_REDIS_ADDR" envDefault:"localhost:6379" validate:"hostname_port"`
	RedisSentinelAddrs  []string `env:"GOERP_REDIS_SENTINEL_ADDRS" validate:"dive,hostname_port"`
	RedisSentinelMaster string   `env:"GOERP_REDIS_SENTINEL_MASTER" envDefault:"mymaster"`
	RedisDB             int      `env:"GOERP_REDIS_DB" envDefault:"0" validate:"min=0"`
	RedisMaxRetries     int      `env:"GOERP_REDIS_MAX_RETRIES" envDefault:"3" validate:"min=0"`

	// Search
	MeilisearchURL    string `env:"GOERP_MEILISEARCH_URL" validate:"omitempty,http_url"`
	MeilisearchAPIKey string `env:"GOERP_MEILISEARCH_API_KEY"`

	// Events and jobs
	SchemaSyncDDLStatementTimeout time.Duration `env:"GOERP_SCHEMA_SYNC_STATEMENT_TIMEOUT" envDefault:"30s"`
	SchemaSyncConcurrency         int           `env:"GOERP_SCHEMA_SYNC_CONCURRENCY" envDefault:"8"`
	QueueCriticalConcurrency      int           `env:"GOERP_QUEUE_CRITICAL_CONCURRENCY" envDefault:"5"`
	QueueDefaultConcurrency       int           `env:"GOERP_QUEUE_DEFAULT_CONCURRENCY" envDefault:"10"`
	QueueBulkConcurrency          int           `env:"GOERP_QUEUE_BULK_CONCURRENCY" envDefault:"20"`
	QueueSearchConcurrency        int           `env:"GOERP_QUEUE_SEARCH_CONCURRENCY" envDefault:"5"`
	QueueEmailConcurrency         int           `env:"GOERP_QUEUE_EMAIL_CONCURRENCY" envDefault:"5"`
	QueueAdminConcurrency         int           `env:"GOERP_QUEUE_ADMIN_CONCURRENCY" envDefault:"5"`
	QueueEventsConcurrency        int           `env:"GOERP_QUEUE_EVENTS_CONCURRENCY" envDefault:"10"`

	// Security
	SecretsBackend string `env:"GOERP_SECRETS_BACKEND" envDefault:"env" validate:"oneof=env vault aws_secretsmanager"`
	EnableAPIKeys  bool   `env:"GOERP_ENABLE_API_KEYS" envDefault:"false"`

	// Registration
	StorageBackend      string   `env:"GOERP_STORAGE_BACKEND" envDefault:"local" validate:"oneof=local seaweedfs s3 r2 gcs"`
	StorageBucket       string   `env:"GOERP_STORAGE_BUCKET" envDefault:"goerp-files"`
	StorageMaxFileBytes int64    `env:"GOERP_STORAGE_MAX_FILE_BYTES" envDefault:"104857600"`
	StorageAllowedTypes []string `env:"GOERP_STORAGE_ALLOWED_TYPES"`
	StorageBlockedTypes []string `env:"GOERP_STORAGE_BLOCKED_TYPES" envDefault:"application/x-executable,application/x-msdownload,application/x-msdos-program,application/x-sh,application/x-bat,application/vnd.microsoft.portable-executable"`

	PoolWarmSize      int           `env:"GOERP_POOL_WARM_SIZE" envDefault:"4"`
	PoolMaxSize       int           `env:"GOERP_POOL_MAX_SIZE" envDefault:"16"`
	PoolBorrowTimeout time.Duration `env:"GOERP_POOL_BORROW_TIMEOUT" envDefault:"5s"`
	PoolMaxMemoryByes uint32        `env:"GOERP_POOL_MAX_MEMORY_BYTES" envDefault:"16777216"`

	// SMTP (invite emails) — defaults match compose.dev.yml's Mailpit
	// (localhost:1025, no auth); a real deployment overrides Host/Port/
	// User/Pass to point at its own relay.
	SMTPHost   string `env:"GOERP_SMTP_HOST" envDefault:"localhost"`
	SMTPPort   int    `env:"GOERP_SMTP_PORT" envDefault:"1025"`
	SMTPUser   string `env:"GOERP_SMTP_USER"`
	SMTPPass   string `env:"GOERP_SMTP_PASSWORD"`
	SMTPFrom   string `env:"GOERP_SMTP_FROM" envDefault:"noreply@goerp.local"`
	AppBaseURL string `env:"GOERP_APP_BASE_URL" envDefault:"http://localhost:8080"`

	// Observability — engine-internals.md's own env var table names these
	// two bare (not GOERP_-prefixed) to match the standard OTel SDK
	// environment variable names every OTel language SDK and collector
	// config already recognizes. OTelExporterOTLPEndpoint's empty default
	// (the doc's own "—") means tracing is off — a no-op tracer — unless
	// a deployment or developer explicitly opts in. OTelInsecure isn't in
	// that table but is itself a standard OTel env var name; defaulted to
	// true since compose.dev.yml's own Jaeger accepts plain-text OTLP.
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

	err := validate.Struct(cfg)

	return cfg, err
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
