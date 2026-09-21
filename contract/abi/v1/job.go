package abi

// MigrationJobPayload is the wire shape a data migration job carries into a
// module's handle_job export.
type MigrationJobPayload struct {
	Handler     string `msgpack:"handler"`
	TenantID    string `msgpack:"tenant_id"`
	FromVersion string `msgpack:"from_version"`
	ToVersion   string `msgpack:"to_version"`
}
