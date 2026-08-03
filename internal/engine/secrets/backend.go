// Package secrets provides the engine's SecretsBackend abstraction: a small
// Get/Set/Rotate interface backing GOERP_SECRETS_BACKEND, with an env-var
// implementation for local development and stubs for vault/aws_secretsmanager
// that fail fast with a clear "not yet supported" error rather than silently
// falling back to env. See security-model.md §6 for the full backend design,
// including how the engine authenticates to Vault/AWS themselves once those
// land.
package secrets

import (
	"context"
	"errors"
)

var (
	ErrUnknownBackend     = errors.New("unknown secrets backend")
	ErrSetNotSupported    = errors.New("set not supported by this backend")
	ErrRotateNotSupported = errors.New("rotate not supported by this backend")
)

type Backend interface {
	Get(ctx context.Context, key string) (string, error)
	Set(ctx context.Context, key, value string) error
	Rotate(ctx context.Context, key string) (string, error)
}

func New(backendType string) (Backend, error) {
	switch backendType {
	case "env":
		return &EnvBackend{}, nil
	case "vault":
		return newVaultBackend()
	case "aws_secretsmanager":
		return newAwsBackend()
	default:
		return nil, ErrUnknownBackend
	}
}
