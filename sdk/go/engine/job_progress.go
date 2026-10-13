package engine

import (
	"encoding/json/v2"
	"fmt"

	abi "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/sdk/go/internal/hostcall"
)

// ReportProgress records progress without waiting for storage. Percent is clamped
// to 0-100 and message to 500 characters. Anonymous, migration, synchronous
// provider and non-job invocations record nothing.
func (ctx *JobContext) ReportProgress(percent int, message string) {
	_ = hostcall.Do(hostJobsReportProgress, abi.JobsReportProgressInput{Percent: percent, Message: message}, nil)
}

// SetResult JSON-encodes v and records it only if the attempt succeeds. The last
// call wins; values over 256 KiB return jobs.result_too_large. The result is
// delivered to the initiating user without field filtering. Invocations that
// cannot report progress record no result.
func (ctx *JobContext) SetResult(v any) error {
	value, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("encode job result: %w", err)
	}
	return hostcall.Do(hostJobsReportResult, abi.JobsReportResultInput{Value: value}, nil)
}
