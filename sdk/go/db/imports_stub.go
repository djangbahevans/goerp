//go:build !wasip1

// Non-wasip1 builds back the host.db imports with panicking stubs so
// `go build`/`go vet`/`go test` work on the host machine without a real
// WASM host to call — there is no meaningful mock for a raw host import
// the way sdk/go/engine's mem_stub.go mocks module memory with an
// in-process arena, since these calls cross into the engine itself.
// Reaching one of these outside a real wasip1-compiled module is a
// programming error, not a case to degrade gracefully from.
package db

func hostDBBegin(ptr, size uint32) uint64 {
	panic("sdk/go/db: host.db.begin is only available in a wasip1 build")
}

func hostDBCommit(ptr, size uint32) uint64 {
	panic("sdk/go/db: host.db.commit is only available in a wasip1 build")
}

func hostDBRollback(ptr, size uint32) uint64 {
	panic("sdk/go/db: host.db.rollback is only available in a wasip1 build")
}

func hostDBQuery(ptr, size uint32) uint64 {
	panic("sdk/go/db: host.db.query is only available in a wasip1 build")
}

func hostDBQueryReplica(ptr, size uint32) uint64 {
	panic("sdk/go/db: host.db.query_replica is only available in a wasip1 build")
}

func hostDBExec(ptr, size uint32) uint64 {
	panic("sdk/go/db: host.db.exec is only available in a wasip1 build")
}

func hostDBExecBatch(ptr, size uint32) uint64 {
	panic("sdk/go/db: host.db.exec_batch is only available in a wasip1 build")
}

func hostDBLock(ptr, size uint32) uint64 {
	panic("sdk/go/db: host.db.lock is only available in a wasip1 build")
}

func hostDBNotify(ptr, size uint32) uint64 {
	panic("sdk/go/db: host.db.notify is only available in a wasip1 build")
}

func hostDBMigrationDDL(ptr, size uint32) uint64 {
	panic("sdk/go/db: host.db.migration_ddl is only available in a wasip1 build")
}
