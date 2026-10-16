package jobqueue

import (
	"context"
	"errors"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/metric"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
)

func metricsMiddleware(metrics *metric.Metrics) river.WorkerMiddlewareFunc {
	return func(ctx context.Context, job *rivertype.JobRow, inner func(context.Context) error) (err error) {
		// Module jobs resolve declared names in their worker; River admits only registered kinds here.
		if job.Kind == (WASMJobArgs{}).Kind() {
			return inner(ctx)
		}
		start := time.Now()
		defer func() {
			if value := recover(); value != nil {
				metrics.Job("", job.Kind, "panic", time.Since(start))
				panic(value)
			}
			status := "success"
			if err != nil {
				status = "error"
			}
			if _, ok := errors.AsType[*river.JobCancelError](err); ok {
				status = "cancelled"
			}
			metrics.Job("", job.Kind, status, time.Since(start))
		}()
		return inner(metric.WithModule(ctx, ""))
	}
}
