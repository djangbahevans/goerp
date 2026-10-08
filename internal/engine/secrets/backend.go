// Package secrets provides secret retrieval, persistence and rotation through the
// configured backend. Environment variables support local development; remote backends
// authenticate independently.
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
