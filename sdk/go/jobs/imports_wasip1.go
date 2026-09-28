//go:build wasip1

package jobs

//go:wasmimport host.jobs enqueue
func hostJobsEnqueue(ptr, size uint32) uint64

//go:wasmimport host.jobs enqueue_tx
func hostJobsEnqueueTx(ptr, size uint32) uint64
