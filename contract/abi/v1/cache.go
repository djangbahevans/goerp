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
