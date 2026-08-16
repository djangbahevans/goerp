package gateway

import (
	"net/netip"
	"time"

	"github.com/caarlos0/env/v11"
	"github.com/go-playground/validator/v10"
)

type Config struct {
	Addr                   netip.AddrPort `env:"GOERP_ADMIN_GATEWAY_ADDR" envDefault:"0.0.0.0:8443"`
	Upstream               string         `env:"GOERP_ADMIN_GATEWAY_UPSTREAM" envDefault:"http://127.0.0.1:8081" validate:"http_url"`
	TLSCertFile            string         `env:"GOERP_ADMIN_GATEWAY_TLS_CERT_FILE" validate:"required"`
	TLSKeyFile             string         `env:"GOERP_ADMIN_GATEWAY_TLS_KEY_FILE" validate:"required"`
	ClientCAFile           string         `env:"GOERP_ADMIN_GATEWAY_CLIENT_CA_FILE" validate:"required"`
	RevocationPollInterval time.Duration  `env:"GOERP_ADMIN_GATEWAY_REVOCATION_POLL_INTERVAL" envDefault:"10s"`
	SecretsBackend         string         `env:"GOERP_SECRETS_BACKEND" envDefault:"env" validate:"oneof=env vault aws_secretsmanager"`
	ShutdownTimeout        time.Duration  `env:"GOERP_ADMIN_GATEWAY_SHUTDOWN_TIMEOUT" envDefault:"10s"`
}

func LoadConfig() (*Config, error) {
	validate := validator.New(validator.WithRequiredStructEnabled())

	cfg := &Config{}

	if err := env.Parse(cfg); err != nil {
		return nil, err
	}

	err := validate.Struct(cfg)

	return cfg, err
}
