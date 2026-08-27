//go:build wasip1

package db

//go:wasmimport host.db begin
func hostDBBegin(ptr, size uint32) uint64

//go:wasmimport host.db commit
func hostDBCommit(ptr, size uint32) uint64

//go:wasmimport host.db rollback
func hostDBRollback(ptr, size uint32) uint64
