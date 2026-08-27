//go:build wasip1

package orm

//go:wasmimport host.orm search
func hostORMSearch(ptr, size uint32) uint64

//go:wasmimport host.orm search_read
func hostORMSearchRead(ptr, size uint32) uint64

//go:wasmimport host.orm read
func hostORMRead(ptr, size uint32) uint64

//go:wasmimport host.orm create
func hostORMCreate(ptr, size uint32) uint64

//go:wasmimport host.orm create_batch
func hostORMCreateBatch(ptr, size uint32) uint64

//go:wasmimport host.orm first_or_create
func hostORMFirstOrCreate(ptr, size uint32) uint64

//go:wasmimport host.orm write
func hostORMWrite(ptr, size uint32) uint64

//go:wasmimport host.orm write_many
func hostORMWriteMany(ptr, size uint32) uint64

//go:wasmimport host.orm write_where
func hostORMWriteWhere(ptr, size uint32) uint64

//go:wasmimport host.orm unlink
func hostORMUnlink(ptr, size uint32) uint64
