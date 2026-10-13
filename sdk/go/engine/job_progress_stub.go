//go:build !wasip1

package engine

func hostJobsReportProgress(ptr, size uint32) uint64 {
	panic("engine.JobContext.ReportProgress requires a wasip1 build")
}

func hostJobsReportResult(ptr, size uint32) uint64 {
	panic("engine.JobContext.SetResult requires a wasip1 build")
}
