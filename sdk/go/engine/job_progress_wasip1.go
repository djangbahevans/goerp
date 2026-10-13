//go:build wasip1

package engine

//go:wasmimport host.jobs report_progress
func hostJobsReportProgress(ptr, size uint32) uint64

//go:wasmimport host.jobs report_result
func hostJobsReportResult(ptr, size uint32) uint64
