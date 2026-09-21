package abi

// AuthzFieldCheckKind selects which of a field's two permissions
// host.authz.field_check evaluates.
type AuthzFieldCheckKind int

const (
	AuthzFieldCheckRead AuthzFieldCheckKind = iota
	AuthzFieldCheckWrite
)

// AuthzFieldCheckInput is the request of host.authz.field_check.
type AuthzFieldCheckInput struct {
	UserID string              `msgpack:"user_id"`
	Model  string              `msgpack:"model"`
	Field  string              `msgpack:"field"`
	Kind   AuthzFieldCheckKind `msgpack:"kind"`
}

// AuthzFieldCheckOutput is the response of host.authz.field_check.
type AuthzFieldCheckOutput struct {
	Allowed bool `msgpack:"allowed"`
}
