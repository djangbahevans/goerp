package engine

func Allocate(size uint32) uint32 { return allocate(size) }

func Deallocate(ptr, size uint32) { deallocate(ptr, size) }

func WriteMem(ptr uint32, data []byte) { writeMem(ptr, data) }

func ReadMem(ptr, size uint32) []byte { return readMem(ptr, size) }
