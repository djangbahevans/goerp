package eventdelivery

import (
	"context"
	"fmt"

	"github.com/djangbahevans/goerp/internal/engine/event"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/registry"
	"github.com/djangbahevans/goerp/internal/engine/role"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	"github.com/djangbahevans/goerp/internal/engine/wasm"
)

type TenantResolver interface {
	GetByID(context.Context, string) (*tenant.Tenant, error)
}

type HandlerInvoker struct {
	Runtime     *wasm.Runtime
	TenantStore TenantResolver
	Roles       *role.Store
}

func (h *HandlerInvoker) invoke(ctx context.Context, snap *registry.RegistrySnapshot, mod *module.LoadedModule, env event.Envelope) (int32, error) {
	tn, err := h.TenantStore.GetByID(ctx, env.TenantID)
	if err != nil {
		return 0, fmt.Errorf("resolve event tenant %s: %w", env.TenantID, err)
	}

	actor, err := h.Roles.ResolveActingUser(ctx, tn.Slug, env.UserID)
	if err != nil {
		return 0, fmt.Errorf("resolve event user %s: %w", env.UserID, err)
	}

	envelope, err := env.Marshal()
	if err != nil {
		return 0, fmt.Errorf("marshal event envelope: %w", err)
	}

	inst, err := mod.Pool.Borrow(ctx)
	if err != nil {
		return 0, fmt.Errorf("borrow instance for %s: %w", mod.Manifest.Name, err)
	}
	defer mod.Pool.Return(inst)

	// Event envelopes carry identity, but do not capture session permissions.
	mc := wasm.NewModuleContext(env.ID, mod.Manifest.Name, env.UserID, actor.ContactID, actor.Roles, nil, env.TenantID, tn.Slug, env.TraceID, mod.Capabilities, h.Runtime.TxLimiter(), wasm.ModuleSnapshot{
		ModelDecls:          mod.ModelDecls,
		FieldSecRegistry:    snap.FieldSecRegistry(),
		EventRegistry:       snap.EventRegistry(),
		DataAuditRegistry:   snap.DataAuditRegistry(),
		ComputedIndex:       snap.ComputedIndex(),
		ComputeTargets:      registry.ComputeTargets(snap),
		PermissionRegistry:  snap.PermissionRegistry(),
		PolicyRegistry:      snap.PolicyRegistry(),
		SearchIndexRegistry: snap.SearchIndexRegistry(),
		OwnedModels:         mod.Manifest.Schema.OwnedModels,
		ExtendsModels:       mod.Manifest.Schema.ExtendsModels,
		ConfigSchema:        mod.Manifest.ConfigSchema,
		UsesConfig:          mod.UsesConfig,
		JobTypes:            mod.Manifest.JobTypes,
		HTTPAllowlist:       mod.Manifest.HTTPAllowlist,
		ORMBulkMaxRows:      h.Runtime.ORMBulkMaxRows(),
		ORMStatementTimeout: h.Runtime.ORMStatementTimeout(),
	})

	inst.SetModuleContext(mc)
	h.Runtime.RegisterInstance(inst)

	defer func() {
		h.Runtime.UnregisterInstance(inst)
		mc.RollbackAll()
		inst.SetModuleContext(nil)
	}()

	status, err := inst.InvokeHandleEvent(ctx, envelope)
	if err != nil {
		return 0, fmt.Errorf("invoke handle_event for %s: %w", mod.Manifest.Name, err)
	}

	return status, nil
}
