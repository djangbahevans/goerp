package abi

// ConfigGetInput is the request of host.config.get.
type ConfigGetInput struct {
	Key string `msgpack:"key"`
}

// ConfigGetOutput is the response of host.config.get. Found is false when
// key has no resolved value; the SDK caller's own default applies then.
type ConfigGetOutput struct {
	Value any  `msgpack:"value"`
	Found bool `msgpack:"found"`
}

// ConfigSetInput is the request of host.config.set.
type ConfigSetInput struct {
	Key   string `msgpack:"key"`
	Value any    `msgpack:"value"`
}

// ConfigSetOutput is the response of host.config.set.
type ConfigSetOutput struct{}
