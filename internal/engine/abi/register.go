package abi

import (
	"context"
	"fmt"

	"github.com/tetratelabs/wazero"
)

// Stateful host namespaces are registered by wasm, which owns their runtime dependencies.
var hostNamespaces = []string{
	"host.connector",
	"host.webhooks",
	"host.workflow",
	"host.analytics",
	"host.time",
	"host.i18n",
	"host.log",
	"host.trace",
	"host.ws",
	"host.ui",
}

func RegisterAll(ctx context.Context, rt wazero.Runtime) error {
	for _, name := range hostNamespaces {
		if _, err := rt.NewHostModuleBuilder(name).Instantiate(ctx); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
	}

	return nil
}
