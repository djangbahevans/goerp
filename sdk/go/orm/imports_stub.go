//go:build !wasip1

// Non-wasip1 builds back the host.orm imports with panicking stubs — see
// sdk/go/db/imports_stub.go's doc comment for why there's no meaningful
// mock here.
package orm

func hostORMSearch(ptr, size uint32) uint64 {
	panic("sdk/go/orm: host.orm.search is only available in a wasip1 build")
}

func hostORMSearchRead(ptr, size uint32) uint64 {
	panic("sdk/go/orm: host.orm.search_read is only available in a wasip1 build")
}

func hostORMRead(ptr, size uint32) uint64 {
	panic("sdk/go/orm: host.orm.read is only available in a wasip1 build")
}

func hostORMCreate(ptr, size uint32) uint64 {
	panic("sdk/go/orm: host.orm.create is only available in a wasip1 build")
}

func hostORMCreateBatch(ptr, size uint32) uint64 {
	panic("sdk/go/orm: host.orm.create_batch is only available in a wasip1 build")
}

func hostORMFirstOrCreate(ptr, size uint32) uint64 {
	panic("sdk/go/orm: host.orm.first_or_create is only available in a wasip1 build")
}

func hostORMWrite(ptr, size uint32) uint64 {
	panic("sdk/go/orm: host.orm.write is only available in a wasip1 build")
}

func hostORMWriteMany(ptr, size uint32) uint64 {
	panic("sdk/go/orm: host.orm.write_many is only available in a wasip1 build")
}

func hostORMWriteWhere(ptr, size uint32) uint64 {
	panic("sdk/go/orm: host.orm.write_where is only available in a wasip1 build")
}

func hostORMUnlink(ptr, size uint32) uint64 {
	panic("sdk/go/orm: host.orm.unlink is only available in a wasip1 build")
}
