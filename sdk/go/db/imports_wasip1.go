//go:build wasip1

package db

//go:wasmimport host.db begin
func hostDBBegin(ptr, size uint32) uint64

//go:wasmimport host.db commit
func hostDBCommit(ptr, size uint32) uint64

//go:wasmimport host.db rollback
func hostDBRollback(ptr, size uint32) uint64

//go:wasmimport host.db query
func hostDBQuery(ptr, size uint32) uint64

//go:wasmimport host.db query_replica
func hostDBQueryReplica(ptr, size uint32) uint64

//go:wasmimport host.db exec
func hostDBExec(ptr, size uint32) uint64

//go:wasmimport host.db exec_batch
func hostDBExecBatch(ptr, size uint32) uint64

//go:wasmimport host.db lock
func hostDBLock(ptr, size uint32) uint64

//go:wasmimport host.db notify
func hostDBNotify(ptr, size uint32) uint64
