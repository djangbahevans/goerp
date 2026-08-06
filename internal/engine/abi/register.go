package abi

import (
	"context"
	"fmt"

	"github.com/tetratelabs/wazero"
)

func RegisterAll(ctx context.Context, rt wazero.Runtime) error {
	for name, fn := range map[string]func(context.Context, wazero.Runtime) error{
		"host.db": func(ctx context.Context, r wazero.Runtime) error {
			_, err := r.NewHostModuleBuilder("host.db").Instantiate(ctx)
			return err
		},
	} {
		if err := fn(ctx, rt); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
	}

	return nil
}
