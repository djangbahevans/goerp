package engine

import (
	"context"
	"fmt"

	"github.com/djangbahevans/goerp/internal/engine/hotreload"
	"github.com/djangbahevans/goerp/internal/engine/loader"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/rs/zerolog/log"
)

// stubHotReloadLeader and stubHotReloadFollower are goerp#452's own
// placeholder LeaderFunc/FollowerFunc — the leader and follower paths
// themselves are goerp#467 and goerp#490's scope, not this ticket's. Both
// always fail, so a hot reload trigger never silently appears to succeed
// before its real implementation lands: a leader-election winner logs
// and returns an error (the trigger's own onChanged already treats that
// as "leader failed," so no announcement is ever published and every
// waiting follower correctly times out and stays on the old version, per
// docs/engine-internals.md §10's leader-crash-recovery semantics).
func stubHotReloadLeader(_ context.Context, moduleName string, _ loader.Source, m manifest.Manifest) error {
	log.Warn().Str("module", moduleName).Str("version", m.Version).
		Msg("hot reload: leader path not yet implemented (goerp#467)")
	return fmt.Errorf("hot reload leader path not yet implemented (goerp#467)")
}

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
