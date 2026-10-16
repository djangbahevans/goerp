package wasm

import (
	"github.com/djangbahevans/goerp/internal/engine/metric"
	"github.com/prometheus/client_golang/prometheus"
)

// WithMetrics registers runtime metrics once for all module pool generations.
func WithMetrics(reg prometheus.Registerer) Option {
	return WithEngineMetrics(metric.New(reg))
}

// WithEngineMetrics shares execution metrics with the engine's other components.
func WithEngineMetrics(metrics *metric.Metrics) Option {
	return func(r *Runtime) { r.metrics = metrics }
}
