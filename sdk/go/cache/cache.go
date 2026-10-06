// Package cache is sdk/go's typed caching API over the host.cache namespace
// (host-abi-reference.md §7, go-sdk-reference.md §8). A cache is declared once
// with Define, which binds its name, key function and TTL; reading, writing
// and invalidating are methods on that value, so a key is built in exactly one
// place and a load and its invalidation cannot disagree.
package cache

import (
	"fmt"
	"strings"
	"time"

	abi "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/sdk/go/internal/hostcall"
	"github.com/vmihailenco/msgpack/v5"
)

// Option configures Define.
type Option func(*definition)

// SetOption configures a single Set.
type SetOption func(*setOptions)

type definition struct {
	ttl time.Duration
}

type setOptions struct {
	ttl time.Duration
}

// TTL sets a cache's default time-to-live. It is required and must be at
// least one second, the host's TTL resolution.
func TTL(d time.Duration) Option {
	return func(def *definition) { def.ttl = d }
}

// WithTTL overrides the cache's default TTL for one Set.
func WithTTL(d time.Duration) SetOption {
	return func(o *setOptions) { o.ttl = d }
}

// host is the seam over the host.cache imports; tests replace it.
var host = hostCalls{
	get: func(in abi.CacheGetInput) (out abi.CacheGetOutput, err error) {
		err = hostcall.Do(hostCacheGet, in, &out)
		return out, err
	},
	set: func(in abi.CacheSetInput) error {
		return hostcall.Do(hostCacheSet, in, nil)
	},
	delete: func(in abi.CacheDeleteInput) error {
		return hostcall.Do(hostCacheDelete, in, nil)
	},
	invalidatePrefix: func(in abi.CacheInvalidatePrefixInput) error {
		return hostcall.Do(hostCacheInvalidatePrefix, in, nil)
	},
	getOrSet: func(in abi.CacheGetOrSetInput) (out abi.CacheGetOrSetOutput, err error) {
		err = hostcall.Do(hostCacheGetOrSet, in, &out)
		return out, err
	},
}

type hostCalls struct {
	get              func(abi.CacheGetInput) (abi.CacheGetOutput, error)
	set              func(abi.CacheSetInput) error
	delete           func(abi.CacheDeleteInput) error
	invalidatePrefix func(abi.CacheInvalidatePrefixInput) error
	getOrSet         func(abi.CacheGetOrSetInput) (abi.CacheGetOrSetOutput, error)
}

// names holds every defined cache name; a module's init() runs
// single-threaded, so it needs no lock.
var names = map[string]struct{}{}

// Cache is a typed value cache: A derives the key, V is the stored value. The
// full key within the module's namespace is "{name}:{key(args)}".
type Cache[A, V any] struct {
	name string
	key  func(A) string
	ttl  time.Duration
}

// Define declares a cache, called in init() or a package-level var. It panics
// when name is empty or contains ':', key is nil, no TTL of at least one
// second is given, or name is already defined in this module. A ':' in name
// would let InvalidateAll's "{name}:" prefix match another cache's entries.
func Define[A, V any](name string, key func(A) string, opts ...Option) Cache[A, V] {
	var def definition
	for _, opt := range opts {
		opt(&def)
	}

	switch {
	case name == "" || strings.Contains(name, ":"):
		panic(fmt.Sprintf("cache.Define: name %q must be non-empty and contain no ':'", name))
	case key == nil:
		panic(fmt.Sprintf("cache.Define: cache %q needs a key function", name))
	case def.ttl < time.Second:
		panic(fmt.Sprintf("cache.Define: cache %q needs cache.TTL of at least one second", name))
	}
	if _, dup := names[name]; dup {
		panic(fmt.Sprintf("cache.Define: cache %q is already defined in this module", name))
	}
	names[name] = struct{}{}

	return Cache[A, V]{name: name, key: key, ttl: def.ttl}
}

// Name returns the cache's name.
func (c Cache[A, V]) Name() string { return c.name }

// Key returns the entry's key within the module's namespace for args,
// "{name}:{key(args)}". Test harnesses use it to locate an entry; the module
// never supplies the tenant and module prefix the host adds.
func (c Cache[A, V]) Key(args A) string {
	return c.name + ":" + c.key(args)
}

func ttlSeconds(d time.Duration) int64 {
	return int64(d / time.Second)
}

// Lookup returns the cached value for args. found is false on a miss, and
// always when the cache is unavailable.
func (c Cache[A, V]) Lookup(args A) (value V, found bool, err error) {
	out, err := host.get(abi.CacheGetInput{Key: c.Key(args)})
	if err != nil || !out.Found {
		return value, false, err
	}
	if err := msgpack.Unmarshal(out.Value, &value); err != nil {
		var zero V
		return zero, false, fmt.Errorf("decode cached %s value: %w", c.name, err)
	}
	return value, true, nil
}

// Set stores v for args with the cache's TTL, or WithTTL's, which must be at
// least one second. It does nothing when the cache is unavailable.
func (c Cache[A, V]) Set(args A, v V, opts ...SetOption) error {
	o := setOptions{ttl: c.ttl}
	for _, opt := range opts {
		opt(&o)
	}

	// The host reads a TTL of 0 as "never expires", so a sub-second override
	// must not truncate to it.
	if o.ttl < time.Second {
		return fmt.Errorf("cache %s: WithTTL must be at least one second, got %s", c.name, o.ttl)
	}

	data, err := msgpack.Marshal(v)
	if err != nil {
		return fmt.Errorf("encode %s value: %w", c.name, err)
	}
	return host.set(abi.CacheSetInput{Key: c.Key(args), Value: data, TTLSeconds: ttlSeconds(o.ttl)})
}

// Invalidate deletes the entry for args.
func (c Cache[A, V]) Invalidate(args A) error {
	return host.delete(abi.CacheDeleteInput{Key: c.Key(args)})
}

// InvalidateAll deletes every entry of this cache and no other cache's.
func (c Cache[A, V]) InvalidateAll() error {
	return host.invalidatePrefix(abi.CacheInvalidatePrefixInput{Prefix: c.name + ":"})
}

// LoadingCache is a Cache with a loader that computes a missing entry.
type LoadingCache[A, V any] struct {
	Cache[A, V]
}

// Loader attaches load to the cache, called in init() or a package-level var.
// The host runs it through the module's handle_cache_loader export, which the
// module must export as DispatchLoader. It panics when the cache already has a
// loader.
func (c Cache[A, V]) Loader(load func(A) (V, error)) LoadingCache[A, V] {
	if _, dup := loaders[c.name]; dup {
		panic(fmt.Sprintf("cache.Loader: cache %q already has a loader", c.name))
	}
	loaders[c.name] = func(argBytes []byte) ([]byte, error) {
		var args A
		if err := msgpack.Unmarshal(argBytes, &args); err != nil {
			return nil, fmt.Errorf("decode %s loader arguments: %w", c.name, err)
		}
		v, err := load(args)
		if err != nil {
			return nil, err
		}
		return msgpack.Marshal(v)
	}
	return LoadingCache[A, V]{Cache: c}
}

// Get returns the cached value for args, running the loader on a miss. The
// host runs the loader under a per-key lock, so concurrent misses for one key
// load once. When the cache is unavailable the loader runs directly and its
// result is not cached.
func (c LoadingCache[A, V]) Get(args A) (value V, err error) {
	argBytes, err := msgpack.Marshal(args)
	if err != nil {
		return value, fmt.Errorf("encode %s loader arguments: %w", c.name, err)
	}

	out, err := host.getOrSet(abi.CacheGetOrSetInput{
		Key:          c.Key(args),
		TTLSeconds:   ttlSeconds(c.ttl),
		LoaderFnName: c.name,
		LoaderArgs:   argBytes,
	})
	if err != nil {
		return value, err
	}
	if err := msgpack.Unmarshal(out.Value, &value); err != nil {
		var zero V
		return zero, fmt.Errorf("decode %s value: %w", c.name, err)
	}
	return value, nil
}
