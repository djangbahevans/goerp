package abi

// CacheGetInput is the request of host.cache.get.
type CacheGetInput struct {
	Key string `msgpack:"key"`
}

// CacheGetOutput is the response of host.cache.get. Value is nil and Found
// false on a miss. TTLRemainingMS is nil when the entry has no expiry.
type CacheGetOutput struct {
	Value          []byte `msgpack:"value"`
	Found          bool   `msgpack:"found"`
	TTLRemainingMS *int64 `msgpack:"ttl_remaining_ms"`
}

// CacheSetInput is the request of host.cache.set. TTLSeconds 0 means the
// entry never expires.
type CacheSetInput struct {
	Key        string `msgpack:"key"`
	Value      []byte `msgpack:"value"`
	TTLSeconds int64  `msgpack:"ttl_seconds"`
}

// CacheSetOutput is the response of host.cache.set.
type CacheSetOutput struct{}

// CacheDeleteInput is the request of host.cache.delete.
type CacheDeleteInput struct {
	Key string `msgpack:"key"`
}

// CacheDeleteOutput is the response of host.cache.delete.
type CacheDeleteOutput struct{}

// CacheInvalidatePrefixInput is the request of host.cache.invalidate_prefix.
type CacheInvalidatePrefixInput struct {
	Prefix string `msgpack:"prefix"`
}

// CacheInvalidatePrefixOutput is the response of host.cache.invalidate_prefix.
type CacheInvalidatePrefixOutput struct{}

// CacheGetOrSetInput is the request of host.cache.get_or_set. LoaderFnName
// is the cache definition's name, which the module's SDK resolves to the
// loader attached to that definition; LoaderArgs is what that loader
// receives.
type CacheGetOrSetInput struct {
	Key          string `msgpack:"key"`
	TTLSeconds   int64  `msgpack:"ttl_seconds"`
	LoaderFnName string `msgpack:"loader_fn_name"`
	LoaderArgs   []byte `msgpack:"loader_args"`
}

// CacheGetOrSetOutput is the response of host.cache.get_or_set.
type CacheGetOrSetOutput struct {
	Value []byte `msgpack:"value"`
}

// CacheLoaderRequest is what the engine sends a module's handle_cache_loader
// export on a cache miss.
type CacheLoaderRequest struct {
	LoaderFnName string `msgpack:"loader_fn_name"`
	LoaderArgs   []byte `msgpack:"loader_args"`
	TenantID     string `msgpack:"tenant_id,omitempty"`
	UserID       string `msgpack:"user_id,omitempty"`
	TraceID      string `msgpack:"trace_id,omitempty"`
}

// CacheLoaderResponse is what a module's handle_cache_loader export returns:
// the msgpack-encoded value, or the loader's failure.
type CacheLoaderResponse struct {
	Value []byte            `msgpack:"value,omitempty"`
	Error *CacheLoaderError `msgpack:"error,omitempty"`
}

// CacheLoaderError is a cache loader's failure.
type CacheLoaderError struct {
	Code    string `msgpack:"code"`
	Message string `msgpack:"message"`
}
