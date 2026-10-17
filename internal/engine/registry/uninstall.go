package registry

import (
	"context"
	"fmt"
	"maps"
	"slices"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/wasm"
)

// UninstallModule removes a registration and notifies remaining modules. prepare handles
// package, worker and cache cleanup under the publication lock; the caller drains the returned pool.
func (r *ModuleRegistry) UninstallModule(ctx context.Context, rt *wasm.Runtime, name string, invocations []HookInvocation, prepare func(context.Context, *module.LoadedModule) error) (*module.LoadedModule, error) {
	release, err := r.Reserve(name)
	if err != nil {
		return nil, err
	}
	defer release()

	tenantIDs, err := r.SystemEvents.ModuleTenantIDs(ctx, name)
	if err != nil {
		return nil, err
	}

	var removed *module.LoadedModule
	snap, err := r.UpdateWithContext(ctx, func(current map[string]*module.LoadedModule) (map[string]*module.LoadedModule, error) {
		removed = current[name]
		if removed == nil {
			return nil, fmt.Errorf("module %q is not installed", name)
		}
		for _, mod := range current {
			if mod.Status != module.StatusFailed && slices.Contains(mod.Manifest.DependsOn, name) {
				return nil, fmt.Errorf("module %q depends on %q", mod.Manifest.Name, name)
			}
		}
		next := maps.Clone(current)
		delete(next, name)
		validation := &ModuleRegistry{}
		if _, err := validation.Update(next); err != nil {
			return nil, fmt.Errorf("validate module removal: %w", err)
		}

		if prepare != nil {
			if err := prepare(ctx, removed); err != nil {
				return nil, fmt.Errorf("prepare module uninstall: %w", err)
			}
		}

		return next, nil
	})
	if err != nil {
		return nil, err
	}

	for _, tenantID := range tenantIDs {
		if err := r.SystemEvents.Emit(ctx, tenantID, "system.module.uninstalled", map[string]any{"module_name": name}); err != nil {
			return removed, err
		}
	}

	for _, invocation := range invocations {
		invocation.Hook = abiv1.HookModuleUninstalled
		invocation.Module = name
		invocation.Version = ""
		invocation.OldVersion = ""
		snap.NotifyHooks(ctx, rt, invocation)
	}

	return removed, nil
}
