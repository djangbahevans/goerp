package secrets

import (
	"context"
	"os"
)

type EnvBackend struct{}

func (b *EnvBackend) Get(ctx context.Context, key string) (string, error) {
	return os.Getenv(key), nil
}

func (b *EnvBackend) Set(ctx context.Context, key, value string) error {
	return ErrSetNotSupported
}

func (b *EnvBackend) Rotate(ctx context.Context, key string) (string, error) {
	return "", ErrRotateNotSupported
}
