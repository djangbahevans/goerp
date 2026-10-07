package abi

// AuthzFieldCheckKind selects which of a field's two permissions
// host.authz.field_check evaluates.
type AuthzFieldCheckKind int

const (
	AuthzFieldCheckRead AuthzFieldCheckKind = iota
	AuthzFieldCheckWrite
)

// AuthzFieldCheckInput is the request of host.authz.field_check. The host
// answers for the request's own user.
type AuthzFieldCheckInput struct {
	Model string              `msgpack:"model"`
	Field string              `msgpack:"field"`
	Kind  AuthzFieldCheckKind `msgpack:"kind"`
}

// AuthzFieldCheckOutput is the response of host.authz.field_check.
type AuthzFieldCheckOutput struct {
	Allowed bool `msgpack:"allowed"`
}

// AuthzCheckInput is the request of host.authz.check and host.authz.require.
// The host answers for the request's own user.
// ResourceID, when set, names the record whose ABAC policies must also admit
// the caller.
type AuthzCheckInput struct {
	Permission string `msgpack:"permission"`
	ResourceID string `msgpack:"resource_id,omitempty"`
}

// AuthzCheckOutput is the response of host.authz.check. Reason is set only
// when Allowed is false.
type AuthzCheckOutput struct {
	Allowed bool   `msgpack:"allowed"`
	Reason  string `msgpack:"reason,omitempty"`
}
