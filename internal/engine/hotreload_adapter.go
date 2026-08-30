package engine

import (
	"context"

	"github.com/djangbahevans/goerp/internal/engine/hotreload"
)

// moduleReloadAdapter satisfies adminapi.ModuleReloader over a
// hotreload.Coordinator — a distinct method name (TriggerReload vs.
// OnModuleBytesChanged) so adminapi never needs to import hotreload
// directly, the same decoupling ModulesDeps.Install/ModuleInstaller
// already follows for moduleinstall.Installer.
type moduleReloadAdapter struct {
	coordinator *hotreload.Coordinator
}

func (a moduleReloadAdapter) TriggerReload(ctx context.Context, moduleName string, data []byte) {
	a.coordinator.TriggerReload(ctx, moduleName, data)
}
