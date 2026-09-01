// Package orm is sdk/go's outbound module-side caller for the host.orm
// namespace (host-abi-reference.md §5a) — the default way to read and
// write records, with ABAC/field-security/computed-field/constraint
// enforcement automatic (go-sdk-reference.md §6a). Wire types mirror
// internal/engine/wasm/host_orm.go's/host_orm_write.go's own, duplicated
// rather than imported since this compiles into a module's own wasip1
// binary.
package orm
