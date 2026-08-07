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
	ListenAddr         string        `env:"GOERP_LISTEN_ADDR" envDefault:":8080" validate:"hostname_port"`
	AdminAddr          string        `env:"GOERP_ADMIN_ADDR" envDefault:"127.0.0.1:8081" validate:"loopback"`
	Environment        string        `env:"GOERP_ENV" envDefault:"production" validate:"oneof=production staging development"`
	LogLevel           string        `env:"GOERP_LOG_LEVEL" envDefault:"info" validate:"oneof=debug info warn error"`
	LogFormat          string        `env:"GOERP_LOG_FORMAT" envDefault:"json" validate:"oneof=json text"`
	CompilationCache   string        `env:"GOERP_COMPILATION_CACHE" envDefault:"./wasm-cache"`
	ShutdownTimeout    time.Duration `env:"GOERP_SHUTDOWN_TIMEOUT" envDefault:"30s"`
	ShutdownDrainDelay time.Duration `env:"GOERP_SHUTDOWN_DRAIN_DELAY" envDefault:"5s"`

	// Database
	DBPrimaryDSN    string `env:"GOERP_DB_PRIMARY_DSN,required"`
	DBReplicaDSN    string `env:"GOERP_DB_REPLICA_DSN"`
	DBSchemaSyncDSN string `env:"GOERP_DB_SCHEMA_SYNC_DSN"`

	// Redis
	RedisAddr           string   `env:"GOERP_REDIS_ADDR" envDefault:"localhost:6379" validate:"hostname_port"`
	RedisSentinelAddrs  []string `env:"GOERP_REDIS_SENTINEL_ADDRS" validate:"dive,hostname_port"`
	RedisSentinelMaster string   `env:"GOERP_REDIS_SENTINEL_MASTER" envDefault:"mymaster"`
	RedisDB             int      `env:"GOERP_REDIS_DB" envDefault:"0" validate:"min=0"`
	RedisMaxRetries     int      `env:"GOERP_REDIS_MAX_RETRIES" envDefault:"3" validate:"min=0"`

	// Search
	MeilisearchURL    string `env:"GOERP_MEILISEARCH_URL" validate:"omitempty,http_url"`
	MeilisearchAPIKey string `env:"GOERP_MEILISEARCH_API_KEY"`

	// Security
	SecretsBackend string `env:"GOERP_SECRETS_BACKEND" envDefault:"env" validate:"oneof=env vault aws_secretsmanager"`

	// Registration
	StorageBackend string `env:"GOERP_STORAGE_BACKEND" envDefault:"local" validate:"oneof=local seaweedfs s3 r2 gcs"`
	StorageBucket  string `env:"GOERP_STORAGE_BUCKET" envDefault:"goerp-files"`

	PoolWarmSize      int           `env:"GOERP_POOL_WARM_SIZE" envDefault:"4"`
	PoolMaxSize       int           `env:"GOERP_POOL_MAX_SIZE" envDefault:"16"`
	PoolBorrowTimeout time.Duration `env:"GOERP_POOL_BORROW_TIMEOUT" envDefault:"5s"`
	PoolMaxMemoryByes uint32        `env:"GOERP_POOL_MAX_MEMORY_BYTES" envDefault:"16777216"`
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
