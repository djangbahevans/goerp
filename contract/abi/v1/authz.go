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

// AuthzRowFilterInput is the request of host.authz.row_filter. The host
// answers for the request's own user.
type AuthzRowFilterInput struct {
	TableName  string `msgpack:"table_name"`
	Permission string `msgpack:"permission"`
}

// AuthzRowFilterOutput is the response of host.authz.row_filter. SQL is
// empty when no policy restricts the permission on the table, and otherwise
// an "AND (...)" clause over the table's columns whose $1.. placeholders are
// Params.
type AuthzRowFilterOutput struct {
	SQL    string `msgpack:"sql"`
	Params []any  `msgpack:"params,omitempty"`
}
