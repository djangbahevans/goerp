// Package vaultpki wraps Vault's PKI secrets engine — a different engine
// from the KV one internal/engine/secrets.Backend covers, with its own
// issuance/revocation API. Backs goerp admin operators issue-cert/
// revoke-cert (cli-reference.md §10); the client/auth plumbing is shared
// with internal/engine/secrets via NewVaultClient, not re-implemented.
package vaultpki

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/caarlos0/env/v11"
	"github.com/hashicorp/vault/api"

	"github.com/djangbahevans/goerp/internal/engine/secrets"
)

type Config struct {
	Mount string `env:"GOERP_VAULT_PKI_MOUNT" envDefault:"pki"`
	Role  string `env:"GOERP_VAULT_PKI_ROLE" envDefault:"operator"`
}

type Client struct {
	vault *api.Client
	cfg   Config
}

func New() (*Client, error) {
	cfg := Config{}
	if err := env.Parse(&cfg); err != nil {
		return nil, fmt.Errorf("parse vault pki config: %w", err)
	}

	vault, err := secrets.NewVaultClient()
	if err != nil {
		return nil, fmt.Errorf("create vault client: %w", err)
	}

	return &Client{vault: vault, cfg: cfg}, nil
}

type IssuedCert struct {
	CertificatePEM string
	PrivateKeyPEM  string
	SerialNumber   string
}

// IssueCert issues a leaf certificate signed by the PKI mount's CA, with
// the given CN and TTL — Vault's own role (GOERP_VAULT_PKI_ROLE) may cap
// the TTL lower than requested; that's Vault's decision, not enforced here.
func (c *Client) IssueCert(ctx context.Context, cn string, ttl time.Duration) (*IssuedCert, error) {
	path := fmt.Sprintf("%s/issue/%s", c.cfg.Mount, c.cfg.Role)
	secret, err := c.vault.Logical().WriteWithContext(ctx, path, map[string]any{
		"common_name": cn,
		"ttl":         ttl.String(),
	})
	if err != nil {
		return nil, fmt.Errorf("issue certificate for %q: %w", cn, err)
	}
	if secret == nil || secret.Data == nil {
		return nil, errors.New("vault pki issue returned no data")
	}

	certPEM, _ := secret.Data["certificate"].(string)
	keyPEM, _ := secret.Data["private_key"].(string)
	serial, _ := secret.Data["serial_number"].(string)
	if certPEM == "" || keyPEM == "" || serial == "" {
		return nil, errors.New("vault pki issue response missing certificate/private_key/serial_number")
	}

	return &IssuedCert{CertificatePEM: certPEM, PrivateKeyPEM: keyPEM, SerialNumber: serial}, nil
}

// RevokeCert revokes a certificate by serial number, adding it to the
// mount's CRL.
func (c *Client) RevokeCert(ctx context.Context, serial string) error {
	path := fmt.Sprintf("%s/revoke", c.cfg.Mount)
	_, err := c.vault.Logical().WriteWithContext(ctx, path, map[string]any{
		"serial_number": serial,
	})
	if err != nil {
		return fmt.Errorf("revoke certificate %q: %w", serial, err)
	}
	return nil
}

// CRL fetches the mount's current certificate revocation list, PEM-encoded
// — the admin gateway's RevocationChecker (goerp#146, internal/gateway/
// mtls.go) is meant to poll this, though wiring that in is separate scope.
func (c *Client) CRL(ctx context.Context) ([]byte, error) {
	path := fmt.Sprintf("%s/crl/pem", c.cfg.Mount)
	resp, err := c.vault.Logical().ReadRawWithContext(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("fetch CRL: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read CRL response: %w", err)
	}
	return body, nil
}
