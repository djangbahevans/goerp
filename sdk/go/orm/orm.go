// Package orm is sdk/go's outbound module-side caller for the host.orm
// namespace (host-abi-reference.md §5a) — the default way to read and
// write records, with ABAC/field-security/computed-field/constraint
// enforcement automatic (go-sdk-reference.md §6a). Wire types mirror
// internal/engine/wasm/host_orm.go's/host_orm_write.go's own, duplicated
// rather than imported since this compiles into a module's own wasip1
// binary.
//
// FirstOrCreate and Unlink diverge from go-sdk-reference.md §6a's
// documented signatures — the host doesn't support uniqueVals-based
// matching or bulk delete yet (goerp#542, goerp#543). No _Tx
// transaction-scoped variants exist yet either (goerp#544).
package orm
