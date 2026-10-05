package wasm

import (
	"context"
	"math"
	"time"

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
	if input.TTLSeconds < 0 || input.TTLSeconds > cacheMaxTTLSeconds {
		return abiv1.CacheSetOutput{}, &abiv1.HostError{
			Code:    abiv1.ErrCodeCacheInvalidTTL,
			Message: "ttl_seconds must be between 0 and the maximum representable duration",
		}
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
