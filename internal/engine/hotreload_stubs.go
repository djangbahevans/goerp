package engine

import (
	"context"
	"fmt"

	"github.com/djangbahevans/goerp/internal/engine/hotreload"
	"github.com/rs/zerolog/log"
)

// stubHotReloadFollower is goerp#452's own placeholder FollowerFunc — the
// follower path itself is goerp#490's scope, not this ticket's. It always
// fails, so a hot reload trigger never silently appears to succeed on a
// follower instance before the real implementation lands: this instance
// stays on the old version and logs a warning, same as any other follower
// failure (hotreload.Coordinator.OnReloadAnnouncement's own doc comment).
// The leader path (goerp#467) is implemented for real by
// modulereload.Leader — see New's own wiring.
func stubHotReloadFollower(_ context.Context, moduleName, version, _ string) error {
	log.Warn().Str("module", moduleName).Str("version", version).
		Msg("hot reload: follower path not yet implemented (goerp#490)")
	return fmt.Errorf("hot reload follower path not yet implemented (goerp#490)")
}

// moduleReloadAdapter satisfies adminapi.ModuleReloader over a
// hotreload.Coordinator — a distinct method name (TriggerReload vs.
// OnModuleBytesChanged) so adminapi never needs to import hotreload
// directly, the same decoupling ModulesDeps.Install/ModuleInstaller
// already follows for moduleinstall.Installer.
type moduleReloadAdapter struct {
	coordinator *hotreload.Coordinator
}

func (a moduleReloadAdapter) TriggerReload(ctx context.Context, moduleName string, data []byte) {
	a.coordinator.OnModuleBytesChanged(ctx, moduleName, data)
}
