package gateway

import (
	"testing"
	"time"
)

func TestLoadConfig_Defaults(t *testing.T) {
	t.Setenv("GOERP_ADMIN_GATEWAY_TLS_CERT_FILE", "/tmp/cert.pem")
	t.Setenv("GOERP_ADMIN_GATEWAY_TLS_KEY_FILE", "/tmp/key.pem")
	t.Setenv("GOERP_ADMIN_GATEWAY_CLIENT_CA_FILE", "/tmp/ca.pem")

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() error: %v", err)
	}

	if got := cfg.Addr.String(); got != "0.0.0.0:8443" {
		t.Errorf("Addr = %q, want %q", got, "0.0.0.0:8443")
	}
	if cfg.Upstream != "http://127.0.0.1:8081" {
		t.Errorf("Upstream = %q, want %q", cfg.Upstream, "http://127.0.0.1:8081")
	}
	if cfg.RevocationPollInterval != 10*time.Second {
		t.Errorf("RevocationPollInterval = %v, want 10s", cfg.RevocationPollInterval)
	}
	if cfg.SecretsBackend != "env" {
		t.Errorf("SecretsBackend = %q, want %q", cfg.SecretsBackend, "env")
	}
	if cfg.ShutdownTimeout != 10*time.Second {
		t.Errorf("ShutdownTimeout = %v, want 10s", cfg.ShutdownTimeout)
	}
}

func TestLoadConfig_MissingRequiredFieldsFails(t *testing.T) {
	if _, err := LoadConfig(); err == nil {
		t.Fatal("LoadConfig() error = nil, want an error for missing TLSCertFile/TLSKeyFile/ClientCAFile")
	}
}
