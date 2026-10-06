package abi

// ConfigGetInput is the request of host.config.get. Key is the short key
// declared in the caller's own config_schema; the host qualifies it with the
// caller's module name.
type ConfigGetInput struct {
	Key string `msgpack:"key"`
}

// ConfigGetOutput is the response of host.config.get. Found is false when
// key has no resolved value; the SDK caller's own default applies then.
type ConfigGetOutput struct {
	Value any  `msgpack:"value"`
	Found bool `msgpack:"found"`
}

// ConfigSetInput is the request of host.config.set. Key is the short key
// declared in the caller's own config_schema.
type ConfigSetInput struct {
	Key   string `msgpack:"key"`
	Value any    `msgpack:"value"`
}

// ConfigSetOutput is the response of host.config.set.
type ConfigSetOutput struct{}
