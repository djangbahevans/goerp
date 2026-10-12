package registry

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"time"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/permission"
	"github.com/djangbahevans/goerp/internal/engine/wasm"
	"github.com/rs/zerolog/log"
	"github.com/vmihailenco/msgpack/v5"
	"go.opentelemetry.io/otel/trace"
)

type HookEntry struct {
	Module   string
	Handler  string
	Priority int
	Order    int
}

// HookInvocation keeps authorization on the host while carrying the wire payload.
type HookInvocation struct {
	abiv1.HookRequest
	ContactID   string
	Roles       []string
	Permissions permission.PermissionBitfield
}

func buildHooks(modules map[string]*module.LoadedModule) map[string][]HookEntry {
	entries := make(map[string][]HookEntry)
	for name, mod := range modules {
		if mod.Status == module.StatusFailed {
			continue
		}
		for order, hook := range mod.Manifest.Hooks {
			if abiv1.EngineHook(hook.Hook) {
				entries[hook.Hook] = append(entries[hook.Hook], HookEntry{Module: name, Handler: hook.Handler, Priority: hook.EffectivePriority(), Order: order})
			}
		}
	}
	for _, handlers := range entries {
		slices.SortFunc(handlers, func(a, b HookEntry) int {
			return cmp.Or(cmp.Compare(b.Priority, a.Priority), cmp.Compare(a.Module, b.Module), cmp.Compare(a.Order, b.Order))
		})
	}

	return entries
}

// Hooks returns priority-ordered registrations. Callers must treat the slice as read-only.
func (s *RegistrySnapshot) Hooks(name string) []HookEntry {
	if s == nil {
		return nil
	}

	return s.hooks[name]
}

// InvokeHook owns an independent invocation and rolls back open transactions. A route
// handler must release its instance before invoking a hook on the same module.
func (s *RegistrySnapshot) InvokeHook(ctx context.Context, rt *wasm.Runtime, entry HookEntry, invocation HookInvocation) (abiv1.HookResult, error) {
	mod := s.modules[entry.Module]
	if mod == nil || mod.Status == module.StatusFailed || mod.Pool == nil {
		return abiv1.HookResult{}, fmt.Errorf("hook module %q is unavailable", entry.Module)
	}

	inst, err := mod.Pool.Borrow(ctx)
	if err != nil {
		return abiv1.HookResult{}, fmt.Errorf("borrow hook instance: %w", err)
	}
	defer mod.Pool.Return(inst)

	req := invocation.Request
	if req.TraceID == "" {
		if span := trace.SpanContextFromContext(ctx); span.IsValid() {
			req.TraceID = span.TraceID().String()
		}
	}
	invocation.Request = req
	ctx = wasm.WithInvocationTime(ctx, req.RequestedAt)
	mc := wasm.NewModuleContext(req.ID, entry.Module, req.UserID, invocation.ContactID, invocation.Roles, invocation.Permissions, req.TenantID, req.TenantSlug, req.TraceID, mod.Capabilities, rt.TxLimiter(), wasm.ModuleSnapshot{
		ModelDecls:          s.Models(entry.Module),
		FieldSecRegistry:    s.FieldSecRegistry(),
		EventRegistry:       s.EventRegistry(),
		ComputedIndex:       s.ComputedIndex(),
		ComputeTargets:      ComputeTargets(s),
		DataAuditRegistry:   s.DataAuditRegistry(),
		PermissionRegistry:  s.PermissionRegistry(),
		PolicyRegistry:      s.PolicyRegistry(),
		SearchIndexRegistry: s.SearchIndexRegistry(),
		OwnedModels:         mod.Manifest.Schema.OwnedModels,
		ExtendsModels:       mod.Manifest.Schema.ExtendsModels,
		ConfigSchema:        mod.Manifest.ConfigSchema,
		UsesConfig:          mod.UsesConfig,
		JobTypes:            mod.Manifest.JobTypes,
		HTTPAllowlist:       mod.Manifest.HTTPAllowlist,
		ORMBulkMaxRows:      rt.ORMBulkMaxRows(),
		ORMStatementTimeout: rt.ORMStatementTimeout(),
	})
	if req.Locale != "" || invocation.Hook == abiv1.HookRequestBefore || invocation.Hook == abiv1.HookRequestAfter {
		mc.SetLocale(abiv1.LocaleInfo{
			Locale:    req.Locale,
			Timezone:  req.Timezone,
			Currency:  req.Currency,
			Direction: req.Direction,
		})
	}
	ctx, err = rt.PrepareInvocationLocale(ctx, mc)
	if err != nil {
		return abiv1.HookResult{}, fmt.Errorf("resolve hook locale: %w", err)
	}
	req.Locale, req.Timezone, req.Currency, req.Direction = mc.Locale.Locale, mc.Locale.Timezone, mc.Locale.Currency, mc.Locale.Direction
	invocation.Request = req

	inst.SetModuleContext(mc)
	rt.RegisterInstance(inst)
	defer func() {
		rt.UnregisterInstance(inst)
		mc.RollbackAll()
		inst.SetModuleContext(nil)
	}()

	invocation.Handler = entry.Handler
	payload, err := msgpack.Marshal(invocation.HookRequest)
	if err != nil {
		return abiv1.HookResult{}, fmt.Errorf("marshal hook request: %w", err)
	}
	data, err := inst.InvokeHandleHook(ctx, payload)
	if err != nil {
		return abiv1.HookResult{}, fmt.Errorf("invoke hook %q handler %q: %w", invocation.Hook, entry.Handler, err)
	}

	var result abiv1.HookResult
	if err := msgpack.Unmarshal(data, &result); err != nil {
		return abiv1.HookResult{}, fmt.Errorf("decode hook result: %w", err)
	}
	if result.Error != "" {
		return abiv1.HookResult{}, fmt.Errorf("hook %q handler %q: %s", invocation.Hook, entry.Handler, result.Error)
	}

	return result, nil
}

// NotifyHooks executes every lifecycle notification with a per-handler time budget.
// Failures are logged; a completed lifecycle operation is not rolled back or retried.
func (s *RegistrySnapshot) NotifyHooks(ctx context.Context, rt *wasm.Runtime, invocation HookInvocation) {
	if len(s.Hooks(invocation.Hook)) == 0 {
		return
	}
	if invocation.Request.RequestedAt.IsZero() {
		invocation.Request.RequestedAt = rt.Now()
	}
	for _, entry := range s.Hooks(invocation.Hook) {
		hookCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		result, err := s.InvokeHook(hookCtx, rt, entry, invocation)
		cancel()
		if err == nil && (result.Request != nil || result.Response != nil) {
			err = fmt.Errorf("lifecycle hook must return an empty result")
		}
		if err != nil {
			log.Error().Err(err).Str("hook", invocation.Hook).Str("module", entry.Module).Str("tenant", invocation.Request.TenantSlug).Msg("lifecycle hook failed")
		}
	}
}

func (s *RegistrySnapshot) NotifyModuleChange(ctx context.Context, rt *wasm.Runtime, req abiv1.Request, name, oldVersion, version string) {
	if version == "" || oldVersion == version {
		return
	}
	nameOfHook := abiv1.HookModuleInstalled
	if oldVersion != "" {
		nameOfHook = abiv1.HookModuleUpgraded
	}

	s.NotifyHooks(ctx, rt, HookInvocation{
		Hook:       nameOfHook,
		Request:    req,
		Module:     name,
		Version:    version,
		OldVersion: oldVersion,
	})
}
