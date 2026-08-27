//go:build wasip1

package engine

import "github.com/djangbahevans/goerp/sdk/go/internal/wasmmem"

func allocate(size uint32) uint32 { return wasmmem.Allocate(size) }

func deallocate(ptr, size uint32) { wasmmem.Deallocate(ptr, size) }

func readMem(ptr, size uint32) []byte { return wasmmem.ReadMem(ptr, size) }

func writeMem(ptr uint32, data []byte) { wasmmem.WriteMem(ptr, data) }
