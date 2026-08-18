package abi

import (
	"context"
	"fmt"

	"github.com/tetratelabs/wazero"
)

// hostNamespaces is the complete set of host.* namespaces from
// host-abi-reference.md §4, minus "host.db" and "host.storage" — their
// real functions are registered separately by wasm.registerHostDB/
// registerHostStorage (which need direct access to *sql.DB/storage.Backend
// and Runtime, wasm-package types abi cannot import without an import
// cycle). Every other namespace here still has no functions attached —
// the individual host.*.* functions (host.orm.search_read, etc.) are each
// their own ticket and attach to these builders separately.
var hostNamespaces = []string{
	"host.orm",
	"host.event",
	"host.cache",
	"host.http",
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
