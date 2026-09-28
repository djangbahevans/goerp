//go:build wasip1

package jobs

//go:wasmimport host.jobs enqueue
func hostJobsEnqueue(ptr, size uint32) uint64

//go:wasmimport host.jobs enqueue_tx
func hostJobsEnqueueTx(ptr, size uint32) uint64

//go:wasmimport host.jobs enqueue_provider
func hostJobsEnqueueProvider(ptr, size uint32) uint64

//go:wasmimport host.jobs enqueue_provider_tx
func hostJobsEnqueueProviderTx(ptr, size uint32) uint64

//go:wasmimport host.jobs dispatch_provider_sync
func hostJobsDispatchProviderSync(ptr, size uint32) uint64

//go:wasmimport host.jobs set_result
func hostJobsSetResult(ptr, size uint32) uint64
