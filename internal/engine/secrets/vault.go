package secrets

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/caarlos0/env/v11"
	"github.com/go-playground/validator/v10"
	"github.com/hashicorp/vault/api"
	"github.com/rs/zerolog/log"
)

// kubernetesServiceAccountTokenPath is the standard projected ServiceAccount
// JWT path — no GoERP-specific mount needed (security-model.md §6). A var,
// not a const, so tests can point it at a fixture file.
var kubernetesServiceAccountTokenPath = "/var/run/secrets/kubernetes.io/serviceaccount/token"

const vaultReauthBackoff = 5 * time.Second

type VaultConfig struct {
	VaultAddr            string `env:"GOERP_VAULT_ADDR" validate:"required"`
	VaultAuthMethod      string `env:"GOERP_VAULT_AUTH_METHOD" envDefault:"kubernetes" validate:"oneof=kubernetes approle token"`
	VaultK8sRole         string `env:"GOERP_VAULT_K8S_ROLE" validate:"required_if=VaultAuthMethod kubernetes"`
	VaultApproleRoleId   string `env:"GOERP_VAULT_APPROLE_ROLE_ID" validate:"required_if=VaultAuthMethod approle"`
	VaultApproleSecretId string `env:"GOERP_VAULT_APPROLE_SECRET_ID" validate:"required_if=VaultAuthMethod approle"`
	VaultToken           string `env:"GOERP_VAULT_TOKEN" validate:"required_if=VaultAuthMethod token"`
	VaultKvMount         string `env:"GOERP_VAULT_KV_MOUNT" envDefault:"secret"`
	Environment          string `env:"GOERP_ENV" envDefault:"production"`
}

type vaultBackend struct {
	client *api.Client
	cfg    VaultConfig
}

func newVaultBackend() (Backend, error) {
	client, cfg, err := newAuthenticatedVaultClient()
	if err != nil {
		return nil, err
	}
	return &vaultBackend{client: client, cfg: cfg}, nil
}

// NewVaultClient returns an authenticated Vault client, sharing the same
// auth-method dispatch and lease-renewal machinery newVaultBackend uses —
// for packages needing direct Vault API access beyond the KV-shaped
// Backend interface (e.g. PKI cert issuance, goerp#179).
func NewVaultClient() (*api.Client, error) {
	client, _, err := newAuthenticatedVaultClient()
	return client, err
}

func newAuthenticatedVaultClient() (*api.Client, VaultConfig, error) {
	cfg := VaultConfig{}
	if err := env.Parse(&cfg); err != nil {
		return nil, cfg, fmt.Errorf("parse vault config: %w", err)
	}
	if err := validator.New(validator.WithRequiredStructEnabled()).Struct(cfg); err != nil {
		return nil, cfg, fmt.Errorf("validate vault config: %w", err)
	}

	client, err := api.NewClient(&api.Config{Address: cfg.VaultAddr})
	if err != nil {
		return nil, cfg, fmt.Errorf("create vault client: %w", err)
	}

	// token auth is a static credential — no lease to renew.
	if cfg.VaultAuthMethod == "token" {
		client.SetToken(cfg.VaultToken)
		return client, cfg, nil
	}

	authFn, err := authenticator(client, cfg)
	if err != nil {
		return nil, cfg, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	secret, err := authFn(ctx)
	if err != nil {
		return nil, cfg, fmt.Errorf("vault authentication: %w", err)
	}
	if secret == nil || secret.Auth == nil {
		return nil, cfg, errors.New("vault login returned no auth information")
	}
	client.SetToken(secret.Auth.ClientToken)

	go maintainLease(client, secret, authFn)

	return client, cfg, nil
}

func authenticator(client *api.Client, cfg VaultConfig) (func(ctx context.Context) (*api.Secret, error), error) {
	switch cfg.VaultAuthMethod {
	case "kubernetes":
		return func(ctx context.Context) (*api.Secret, error) {
			jwt, err := os.ReadFile(kubernetesServiceAccountTokenPath)
			if err != nil {
				return nil, fmt.Errorf("read kubernetes service account token: %w", err)
			}
			return client.Logical().WriteWithContext(ctx, "auth/kubernetes/login", map[string]any{
				"role": cfg.VaultK8sRole,
				"jwt":  string(jwt),
			})
		}, nil
	case "approle":
		return func(ctx context.Context) (*api.Secret, error) {
			return client.Logical().WriteWithContext(ctx, "auth/approle/login", map[string]any{
				"role_id":   cfg.VaultApproleRoleId,
				"secret_id": cfg.VaultApproleSecretId,
			})
		}, nil
	default:
		return nil, fmt.Errorf("unsupported vault auth method %q", cfg.VaultAuthMethod)
	}
}

// maintainLease keeps the login token's lease renewed for the lifetime of
// the process, re-authenticating from scratch (security-model.md §6) once
// the lease can no longer be renewed. Runs until the process exits.
func maintainLease(client *api.Client, secret *api.Secret, authFn func(ctx context.Context) (*api.Secret, error)) {
	for {
		watcher, err := client.NewLifetimeWatcher(&api.LifetimeWatcherInput{Secret: secret})
		if err != nil {
			log.Error().Err(err).Msg("vault: create lifetime watcher")
			time.Sleep(vaultReauthBackoff)
			continue
		}

		go watcher.Start()
		waitForRenewalEnd(watcher)
		watcher.Stop()

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		newSecret, err := authFn(ctx)
		cancel()
		if err != nil {
			log.Error().Err(err).Msg("vault: re-authentication failed, retrying")
			time.Sleep(vaultReauthBackoff)
			continue
		}
		secret = newSecret
		client.SetToken(secret.Auth.ClientToken)
	}
}

func waitForRenewalEnd(watcher *api.LifetimeWatcher) {
	for {
		select {
		case err := <-watcher.DoneCh():
			if err != nil {
				log.Warn().Err(err).Msg("vault: lease renewal ended")
			}
			return
		case <-watcher.RenewCh():
		}
	}
}

// secretPath follows security-model.md §6's naming convention:
// {mount}/data/goerp/{env}/{key}, KV v2.
func (v *vaultBackend) secretPath(key string) string {
	return fmt.Sprintf("%s/data/goerp/%s/%s", v.cfg.VaultKvMount, v.cfg.Environment, key)
}

func (v *vaultBackend) Get(ctx context.Context, key string) (string, error) {
	secret, err := v.client.Logical().ReadWithContext(ctx, v.secretPath(key))
	if err != nil {
		return "", fmt.Errorf("read vault secret %q: %w", key, err)
	}
	if secret == nil {
		return "", nil
	}
	data, _ := secret.Data["data"].(map[string]any)
	value, _ := data["value"].(string)
	return value, nil
}

func (v *vaultBackend) Set(ctx context.Context, key, value string) error {
	_, err := v.client.Logical().WriteWithContext(ctx, v.secretPath(key), map[string]any{
		"data": map[string]any{"value": value},
	})
	if err != nil {
		return fmt.Errorf("write vault secret %q: %w", key, err)
	}
	return nil
}

// Rotate replaces key with a fresh random value (32 bytes, base64url) and
// returns it — generic, since callers needing specific key material (e.g.
// RSA keypairs) generate it themselves and call Set, not Rotate.
func (v *vaultBackend) Rotate(ctx context.Context, key string) (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate rotated value: %w", err)
	}
	value := base64.RawURLEncoding.EncodeToString(buf)

	if err := v.Set(ctx, key, value); err != nil {
		return "", err
	}
	return value, nil
}
