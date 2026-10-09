package eventdelivery

import (
	"context"
	"fmt"

	"github.com/djangbahevans/goerp/internal/engine/event"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/registry"
	"github.com/djangbahevans/goerp/internal/engine/role"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
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
	return h.invokeSubscription(ctx, snap, mod, env, false, false)
}

func (h *HandlerInvoker) invokeSubscription(ctx context.Context, snap *registry.RegistrySnapshot, mod *module.LoadedModule, env event.Envelope, transactional, replay bool) (int32, error) {
	tn, err := h.TenantStore.GetByID(ctx, env.TenantID)
	if err != nil {
		return 0, fmt.Errorf("resolve event tenant %s: %w", env.TenantID, err)
	}

	actor, err := h.Roles.ResolveActingUser(ctx, tn.Slug, env.UserID)
	if err != nil {
		return 0, fmt.Errorf("resolve event user %s: %w", env.UserID, err)
	}

	inst, err := mod.Pool.Borrow(ctx)
	if err != nil {
		return 0, fmt.Errorf("borrow instance for %s: %w", mod.Manifest.Name, err)
	}
	defer mod.Pool.Return(inst)

	// Event envelopes carry identity, but do not capture session permissions.
	mc := wasm.NewModuleContext(env.ID, mod.Manifest.Name, env.UserID, actor.ContactID, actor.Roles, nil, env.TenantID, tn.Slug, env.TraceID, mod.Capabilities, h.Runtime.TxLimiter(), wasm.ModuleSnapshot{
		ModelDecls:          snap.Models(mod.Manifest.Name),
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

	var managed *wasm.ManagedTransaction
	if transactional {
		managed, err = h.Runtime.BeginManagedTransaction(ctx, mc)
		if err != nil {
			return 0, fmt.Errorf("open event delivery transaction: %w", err)
		}
		defer managed.Rollback()

		ctx = managed.Context
		conflict := "DO NOTHING"
		if replay {
			conflict = "DO UPDATE SET delivered_at = NOW()"
		}

		// The ledger claim retains the engine role; module SQL borrows the tenant role.
		result, err := managed.Tx.ExecContext(ctx, `INSERT INTO `+tenantschema.Name(tn.Slug)+`.event_deliveries
			(subscriber_module, event_id, emitted_at, event_name, event_version)
			VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT (subscriber_module, event_id, emitted_at) `+conflict,
			mod.Manifest.Name, env.ID, env.EmittedAt, env.Name, env.Version)
		if err != nil {
			return 0, fmt.Errorf("claim event delivery: %w", err)
		}

		rows, err := result.RowsAffected()
		if err != nil {
			return 0, fmt.Errorf("read event delivery claim: %w", err)
		}
		if rows == 0 {
			return 0, nil
		}

		env.TxID = managed.ID
	}

	envelope, err := env.Marshal()
	if err != nil {
		return 0, fmt.Errorf("marshal event envelope: %w", err)
	}

	status, err := inst.InvokeHandleEvent(ctx, envelope)
	if err != nil {
		return 0, fmt.Errorf("invoke handle_event for %s: %w", mod.Manifest.Name, err)
	}

	if managed != nil && status == 0 {
		if err := managed.Commit(); err != nil {
			return 0, fmt.Errorf("commit event delivery: %w", err)
		}
	}

	return status, nil
}
