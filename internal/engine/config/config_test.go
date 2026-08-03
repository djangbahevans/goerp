package config

import (
	"testing"

	"github.com/go-playground/validator/v10"
)

// setRequiredEnv sets the env vars Load() requires to succeed at all
// (GOERP_DB_PRIMARY_DSN), so individual tests can focus on the one value
// they're actually exercising.
func setRequiredEnv(t *testing.T) {
	t.Helper()
	t.Setenv("GOERP_DB_PRIMARY_DSN", "postgres://primary")
}

func TestLoadDefaults(t *testing.T) {
	setRequiredEnv(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if cfg.ListenAddr != ":8080" {
		t.Errorf("ListenAddr = %q, want %q", cfg.ListenAddr, ":8080")
	}
	if cfg.AdminAddr != "127.0.0.1:8081" {
		t.Errorf("AdminAddr = %q, want %q", cfg.AdminAddr, "127.0.0.1:8081")
	}
	if cfg.SecretsBackend != "env" {
		t.Errorf("SecretsBackend = %q, want %q", cfg.SecretsBackend, "env")
	}
}

func TestLoadEnvOverride(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("GOERP_LISTEN_ADDR", ":9999")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if cfg.ListenAddr != ":9999" {
		t.Errorf("ListenAddr = %q, want %q", cfg.ListenAddr, ":9999")
	}
}

func TestLoadDurationField(t *testing.T) {
	setRequiredEnv(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if cfg.ShutdownTimeout.String() != "30s" {
		t.Errorf("ShutdownTimeout default = %s, want 30s", cfg.ShutdownTimeout)
	}

	t.Setenv("GOERP_SHUTDOWN_TIMEOUT", "45s")
	cfg, err = Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.ShutdownTimeout.String() != "45s" {
		t.Errorf("ShutdownTimeout override = %s, want 45s", cfg.ShutdownTimeout)
	}
}

func TestLoadStringSlice(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("GOERP_REDIS_SENTINEL_ADDRS", "host1:26379,host2:26379,host3:26379")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	want := []string{"host1:26379", "host2:26379", "host3:26379"}
	if len(cfg.RedisSentinelAddrs) != len(want) {
		t.Fatalf("RedisSentinelAddrs = %#v, want %#v", cfg.RedisSentinelAddrs, want)
	}
	for i, addr := range want {
		if cfg.RedisSentinelAddrs[i] != addr {
			t.Errorf("RedisSentinelAddrs[%d] = %q, want %q", i, cfg.RedisSentinelAddrs[i], addr)
		}
	}
}

func TestLoadMissingRequired(t *testing.T) {
	// Deliberately not calling setRequiredEnv.
	_, err := Load()
	if err == nil {
		t.Fatal("expected an error when required DSNs are unset, got nil")
	}
}

func TestLoadInvalidHostnamePort(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("GOERP_LISTEN_ADDR", "not a valid addr")

	_, err := Load()
	if err == nil {
		t.Fatal("expected a validation error for malformed GOERP_LISTEN_ADDR, got nil")
	}
}

func TestLoadMeilisearchUrlOptional(t *testing.T) {
	setRequiredEnv(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.MeilisearchURL != "" {
		t.Errorf("MeilisearchURL default = %q, want empty (unconfigured)", cfg.MeilisearchURL)
	}
}

func TestLoadInvalidMeilisearchUrl(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("GOERP_MEILISEARCH_URL", "not-a-url")

	_, err := Load()
	if err == nil {
		t.Fatal("expected a validation error for malformed GOERP_MEILISEARCH_URL, got nil")
	}
}

func TestLoadInvalidOneof(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("GOERP_ENV", "not-a-real-environment")

	_, err := Load()
	if err == nil {
		t.Fatal("expected a validation error for an out-of-range GOERP_ENV, got nil")
	}
}

func TestIsLoopback(t *testing.T) {
	cases := map[string]bool{
		"127.0.0.1":      true,
		"127.0.0.1:8081": true,
		"localhost":      true,
		"localhost:8081": true,
		"::1":            true,
		"0.0.0.0":        false,
		"0.0.0.0:8081":   false,
		"example.com":    false,
		"10.0.0.5":       false,
	}

	validate := validator.New()
	if err := validate.RegisterValidation("loopback", isLoopback); err != nil {
		t.Fatalf("RegisterValidation() error: %v", err)
	}

	for addr, want := range cases {
		t.Run(addr, func(t *testing.T) {
			err := validate.Var(addr, "loopback")
			got := err == nil
			if got != want {
				t.Errorf("isLoopback(%q) = %v, want %v", addr, got, want)
			}
		})
	}
}
