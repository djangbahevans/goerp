package abi

import (
	"context"
	"fmt"

	"github.com/tetratelabs/wazero"
)

// hostNamespaces is the complete set of host.* namespaces from
// host-abi-reference.md §4. Each is registered as its own WASM host module
// with no functions attached yet — the individual host.*.* functions
// (host.db.query, host.orm.search_read, etc.) are each their own ticket and
// attach to these builders separately.
var hostNamespaces = []string{
	"host.db",
	"host.orm",
	"host.event",
	"host.cache",
	"host.http",
	"host.storage",
	"host.jobs",
	"host.connector",
	"host.notify",
	"host.webhooks",
	"host.search",
	"host.authz",
	"host.config",
	"host.workflow",
	"host.analytics",
	"host.crypto",
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
