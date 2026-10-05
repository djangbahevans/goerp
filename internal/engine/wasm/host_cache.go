package wasm

import (
	"context"
	"math"
	"time"
	"uuid"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/djangbahevans/goerp/internal/engine/cache"
	"github.com/rs/zerolog/log"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/vmihailenco/msgpack/v5"
)

const (
	cacheMaxValueBytes = 5 << 20
	cacheMaxTTLSeconds = math.MaxInt64 / int64(time.Second)

	// cacheLoadLockTTL bounds how long a crashed loader can block other
	// callers of the same key.
	cacheLoadLockTTL      = 30 * time.Second
	cacheLoadPollInterval = 20 * time.Millisecond
)

// registerHostCache attaches host.cache to the runtime. A nil cacheClient or
// a failing Redis degrades every call instead of failing it: the cache is an
// optimisation a module must be able to run without.
func registerHostCache(ctx context.Context, rt wazero.Runtime, r *Runtime, cacheClient *cache.Client) error {
	_, err := rt.NewHostModuleBuilder("host.cache").
		NewFunctionBuilder().WithFunc(makeCacheHostFunc(r, cacheClient, CacheGet)).Export("get").
		NewFunctionBuilder().WithFunc(makeCacheHostFunc(r, cacheClient, CacheSet)).Export("set").
		NewFunctionBuilder().WithFunc(makeCacheHostFunc(r, cacheClient, CacheDelete)).Export("delete").
		NewFunctionBuilder().WithFunc(makeCacheHostFunc(r, cacheClient, CacheInvalidatePrefix)).Export("invalidate_prefix").
		NewFunctionBuilder().WithFunc(makeCacheHostFunc(r, cacheClient, func(ctx context.Context, c *cache.Client, modCtx *ModuleContext, in abiv1.CacheGetOrSetInput) (abiv1.CacheGetOrSetOutput, *abiv1.HostError) {
		return CacheGetOrSet(ctx, c, modCtx, in, moduleCacheLoader(r))
	})).Export("get_or_set").
		Instantiate(ctx)
	return err
}

func makeCacheHostFunc[In, Out any](r *Runtime, cacheClient *cache.Client, call func(context.Context, *cache.Client, *ModuleContext, In) (Out, *abiv1.HostError)) func(ctx context.Context, m api.Module, ptr, length uint32) uint64 {
	return func(ctx context.Context, m api.Module, ptr, length uint32) uint64 {
		inst := r.InstanceForModule(m)
		allocate := inst.allocate

		inputBytes, err := abi.ReadFromModule(m.Memory(), ptr, length)
		if err != nil {
			return abi.EncodeHostError(ctx, m, allocate, abi.MemoryFault())
		}
		var input In
		if err := msgpack.Unmarshal(inputBytes, &input); err != nil {
			return abi.EncodeHostError(ctx, m, allocate, abi.DeserializeError(err))
		}

		out, hostErr := call(ctx, cacheClient, inst.ModuleContext(), input)
		if hostErr != nil {
			return abi.EncodeHostError(ctx, m, allocate, hostErr)
		}
		return abi.WriteToModule(ctx, m, allocate, out)
	}
}

// cacheKeyPrefix is the tenant- and module-scoped namespace every host.cache
// key lives under; modules never see or supply it.
func cacheKeyPrefix(modCtx *ModuleContext) string {
	return modCtx.TenantID + ":" + modCtx.ModuleName + ":"
}

func validateCacheTTL(ttlSeconds int64) *abiv1.HostError {
	if ttlSeconds < 0 || ttlSeconds > cacheMaxTTLSeconds {
		return &abiv1.HostError{
			Code:    abiv1.ErrCodeCacheInvalidTTL,
			Message: "ttl_seconds must be between 0 and the maximum representable duration",
		}
	}
	return nil
}

func degradeCache(op string, modCtx *ModuleContext, err error) {
	log.Warn().Err(err).Str("op", op).Str("tenant_id", modCtx.TenantID).Str("module", modCtx.ModuleName).Msg("host.cache degraded; Redis unavailable")
}

func CacheGet(ctx context.Context, cacheClient *cache.Client, modCtx *ModuleContext, input abiv1.CacheGetInput) (abiv1.CacheGetOutput, *abiv1.HostError) {
	if !modCtx.Capabilities().Has(abi.CapCacheRead) {
		return abiv1.CacheGetOutput{}, abi.CapabilityDenied("cache.read")
	}
	if cacheClient == nil {
		return abiv1.CacheGetOutput{}, nil
	}

	value, ttl, found, err := cacheClient.GetWithTTL(ctx, cacheKeyPrefix(modCtx)+input.Key)
	if err != nil {
		degradeCache("get", modCtx, err)
		return abiv1.CacheGetOutput{}, nil
	}
	if !found {
		return abiv1.CacheGetOutput{}, nil
	}

	out := abiv1.CacheGetOutput{Value: []byte(value), Found: true}
	if ttl > 0 {
		out.TTLRemainingMS = new(ttl.Milliseconds())
	}
	return out, nil
}

func CacheSet(ctx context.Context, cacheClient *cache.Client, modCtx *ModuleContext, input abiv1.CacheSetInput) (abiv1.CacheSetOutput, *abiv1.HostError) {
	if !modCtx.Capabilities().Has(abi.CapCacheWrite) {
		return abiv1.CacheSetOutput{}, abi.CapabilityDenied("cache.write")
	}
	if len(input.Value) > cacheMaxValueBytes {
		return abiv1.CacheSetOutput{}, &abiv1.HostError{
			Code:    abiv1.ErrCodeCacheValueTooLarge,
			Message: "cached value exceeds the 5MB maximum",
		}
	}
	if hostErr := validateCacheTTL(input.TTLSeconds); hostErr != nil {
		return abiv1.CacheSetOutput{}, hostErr
	}
	if cacheClient == nil {
		return abiv1.CacheSetOutput{}, nil
	}

	ttl := time.Duration(input.TTLSeconds) * time.Second
	if err := cacheClient.SetWithTTL(ctx, cacheKeyPrefix(modCtx)+input.Key, string(input.Value), ttl); err != nil {
		degradeCache("set", modCtx, err)
	}
	return abiv1.CacheSetOutput{}, nil
}

func CacheDelete(ctx context.Context, cacheClient *cache.Client, modCtx *ModuleContext, input abiv1.CacheDeleteInput) (abiv1.CacheDeleteOutput, *abiv1.HostError) {
	if !modCtx.Capabilities().Has(abi.CapCacheWrite) {
		return abiv1.CacheDeleteOutput{}, abi.CapabilityDenied("cache.write")
	}
	if cacheClient == nil {
		return abiv1.CacheDeleteOutput{}, nil
	}

	if err := cacheClient.Delete(ctx, cacheKeyPrefix(modCtx)+input.Key); err != nil {
		degradeCache("delete", modCtx, err)
	}
	return abiv1.CacheDeleteOutput{}, nil
}

func CacheInvalidatePrefix(ctx context.Context, cacheClient *cache.Client, modCtx *ModuleContext, input abiv1.CacheInvalidatePrefixInput) (abiv1.CacheInvalidatePrefixOutput, *abiv1.HostError) {
	if !modCtx.Capabilities().Has(abi.CapCacheWrite) {
		return abiv1.CacheInvalidatePrefixOutput{}, abi.CapabilityDenied("cache.write")
	}
	if cacheClient == nil {
		return abiv1.CacheInvalidatePrefixOutput{}, nil
	}

	if err := cacheClient.DeleteByPrefix(ctx, cacheKeyPrefix(modCtx)+input.Prefix); err != nil {
		degradeCache("invalidate_prefix", modCtx, err)
	}
	return abiv1.CacheInvalidatePrefixOutput{}, nil
}

// cacheLoader computes the value of a missing key.
type cacheLoader func(ctx context.Context, modCtx *ModuleContext, input abiv1.CacheGetOrSetInput) ([]byte, *abiv1.HostError)

// moduleCacheLoader runs the loader attached to input.LoaderFnName on a fresh
// instance of the calling module, because the caller's own instance is
// mid-host-call and WASM cannot reenter it.
func moduleCacheLoader(r *Runtime) cacheLoader {
	return func(ctx context.Context, modCtx *ModuleContext, input abiv1.CacheGetOrSetInput) ([]byte, *abiv1.HostError) {
		inst, cleanup, hostErr := borrowModuleInstance(ctx, r, modCtx, modCtx.ModuleName)
		if hostErr != nil {
			return nil, hostErr
		}
		defer cleanup()

		payload, err := msgpack.Marshal(abiv1.CacheLoaderRequest{
			LoaderFnName: input.LoaderFnName,
			LoaderArgs:   input.LoaderArgs,
			TenantID:     modCtx.TenantID,
			UserID:       modCtx.UserID,
			TraceID:      modCtx.TraceID,
		})
		if err != nil {
			return nil, &abiv1.HostError{Code: abiv1.ErrCodeUnavailable, Message: err.Error()}
		}

		respBytes, err := inst.InvokeHandleCacheLoader(ctx, payload)
		if err != nil {
			return nil, &abiv1.HostError{Code: abiv1.ErrCodeUnavailable, Message: "cache loader " + input.LoaderFnName + ": " + err.Error()}
		}

		var resp abiv1.CacheLoaderResponse
		if err := msgpack.Unmarshal(respBytes, &resp); err != nil {
			return nil, &abiv1.HostError{Code: abiv1.ErrCodeUnavailable, Message: err.Error()}
		}
		if resp.Error != nil {
			return nil, &abiv1.HostError{Code: resp.Error.Code, Message: resp.Error.Message}
		}
		return resp.Value, nil
	}
}

// CacheGetOrSet returns the cached value for input.Key, or runs load once
// across concurrent callers and caches its result. Callers that lose the
// per-key lock wait for the winner's stored value. When Redis is unavailable
// the loader runs directly and its result is not cached.
func CacheGetOrSet(ctx context.Context, cacheClient *cache.Client, modCtx *ModuleContext, input abiv1.CacheGetOrSetInput, load cacheLoader) (abiv1.CacheGetOrSetOutput, *abiv1.HostError) {
	if !modCtx.Capabilities().Has(abi.CapCacheRead) {
		return abiv1.CacheGetOrSetOutput{}, abi.CapabilityDenied("cache.read")
	}
	if !modCtx.Capabilities().Has(abi.CapCacheWrite) {
		return abiv1.CacheGetOrSetOutput{}, abi.CapabilityDenied("cache.write")
	}
	if hostErr := validateCacheTTL(input.TTLSeconds); hostErr != nil {
		return abiv1.CacheGetOrSetOutput{}, hostErr
	}

	loadUncached := func() (abiv1.CacheGetOrSetOutput, *abiv1.HostError) {
		value, hostErr := load(ctx, modCtx, input)
		return abiv1.CacheGetOrSetOutput{Value: value}, hostErr
	}
	if cacheClient == nil {
		return loadUncached()
	}

	key := cacheKeyPrefix(modCtx) + input.Key
	// The lock key lives outside the module's namespace so a module key can
	// never collide with it and invalidate_prefix never deletes it.
	lockKey := "cacheload:" + key
	token := uuid.NewV7().String()

	for {
		value, _, found, err := cacheClient.GetWithTTL(ctx, key)
		if err != nil {
			degradeCache("get_or_set", modCtx, err)
			return loadUncached()
		}
		if found {
			return abiv1.CacheGetOrSetOutput{Value: []byte(value)}, nil
		}

		acquired, err := cacheClient.SetNXWithTTL(ctx, lockKey, token, cacheLoadLockTTL)
		if err != nil {
			degradeCache("get_or_set", modCtx, err)
			return loadUncached()
		}
		if acquired {
			return loadAndStore(ctx, cacheClient, modCtx, input, load, key, lockKey, token)
		}

		select {
		case <-ctx.Done():
			return abiv1.CacheGetOrSetOutput{}, &abiv1.HostError{Code: abiv1.ErrCodeTimeout, Message: "timed out waiting for another caller's cache load: " + context.Cause(ctx).Error()}
		case <-time.After(cacheLoadPollInterval):
		}
	}
}

// loadAndStore runs load while holding the lock on key, re-checking the cache
// first because another caller may have stored the value and released the
// lock between this caller's miss and its lock acquisition.
func loadAndStore(ctx context.Context, cacheClient *cache.Client, modCtx *ModuleContext, input abiv1.CacheGetOrSetInput, load cacheLoader, key, lockKey, token string) (abiv1.CacheGetOrSetOutput, *abiv1.HostError) {
	defer func() {
		if _, err := cacheClient.DeleteIfEqual(context.WithoutCancel(ctx), lockKey, token); err != nil {
			degradeCache("get_or_set unlock", modCtx, err)
		}
	}()

	if value, found, err := cacheClient.Get(ctx, key); err == nil && found {
		return abiv1.CacheGetOrSetOutput{Value: []byte(value)}, nil
	}

	value, hostErr := load(ctx, modCtx, input)
	if hostErr != nil {
		return abiv1.CacheGetOrSetOutput{}, hostErr
	}
	if len(value) > cacheMaxValueBytes {
		return abiv1.CacheGetOrSetOutput{}, &abiv1.HostError{
			Code:    abiv1.ErrCodeCacheValueTooLarge,
			Message: "loaded value exceeds the 5MB maximum",
		}
	}

	if err := cacheClient.SetWithTTL(ctx, key, string(value), time.Duration(input.TTLSeconds)*time.Second); err != nil {
		degradeCache("get_or_set", modCtx, err)
	}
	return abiv1.CacheGetOrSetOutput{Value: value}, nil
}
