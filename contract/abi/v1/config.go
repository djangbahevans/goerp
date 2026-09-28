package abi

// ConfigGetInput is the request of host.config.get. Key is the fully
// namespaced "{module}.{config_key}" form (manifest-spec.md §17) — a
// module always reads back the same prefixed form it declares in its own
// config_schema, never the bare config_key.
type ConfigGetInput struct {
	Key string `msgpack:"key"`
}

// ConfigGetOutput is the response of host.config.get. Value is decoded to
// the Go/TypeScript type matching the key's declared config_schema
// `type` (manifest-spec.md §17) — a string, int64, float64, bool,
// []string, []int64, []float64, or arbitrary value for `"json"`. Found is
// false, with Value left at its zero value, when none of
// multitenancy-internals.md §7's three resolution tiers (operator
// override, tenant-admin module_config, manifest default) has a value —
// the caller's own SDK-level default, not this struct, supplies a
// fallback in that case (host-abi-reference.md §14).
type ConfigGetOutput struct {
	Value any  `msgpack:"value"`
	Found bool `msgpack:"found"`
}

// ConfigSetInput is the request of host.config.set. Value must already be
// of the Go/TypeScript type matching key's declared config_schema `type`.
type ConfigSetInput struct {
	Key   string `msgpack:"key"`
	Value any    `msgpack:"value"`
}

// ConfigSetOutput is the response of host.config.set — a bare
// acknowledgement, host.config.set has nothing to report back beyond
// success/failure (the SDK's own Set wrapper returns only an error).
type ConfigSetOutput struct{}
