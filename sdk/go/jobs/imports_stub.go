//go:build !wasip1

// Non-wasip1 builds back the host.jobs imports with panicking stubs —
// see sdk/go/db/imports_stub.go's doc comment for why there's no
// meaningful mock here.
package jobs

func hostJobsEnqueue(ptr, size uint32) uint64 {
	panic("sdk/go/jobs: host.jobs.enqueue is only available in a wasip1 build")
}

func hostJobsEnqueueTx(ptr, size uint32) uint64 {
	panic("sdk/go/jobs: host.jobs.enqueue_tx is only available in a wasip1 build")
}

func hostJobsEnqueueProvider(ptr, size uint32) uint64 {
	panic("sdk/go/jobs: host.jobs.enqueue_provider is only available in a wasip1 build")
}

func hostJobsEnqueueProviderTx(ptr, size uint32) uint64 {
	panic("sdk/go/jobs: host.jobs.enqueue_provider_tx is only available in a wasip1 build")
}

func hostJobsDispatchProviderSync(ptr, size uint32) uint64 {
	panic("sdk/go/jobs: host.jobs.dispatch_provider_sync is only available in a wasip1 build")
}

func hostJobsSetResult(ptr, size uint32) uint64 {
	panic("sdk/go/jobs: host.jobs.set_result is only available in a wasip1 build")
}
