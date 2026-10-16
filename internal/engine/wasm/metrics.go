package wasm

import "github.com/prometheus/client_golang/prometheus"

func newPoolBorrowDuration() *prometheus.HistogramVec {
	return prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name: "erp_wasm_pool_borrow_duration_seconds",
		Help: "Time spent acquiring a WASM instance in seconds, including instantiation, for successful borrows.",
	}, []string{"module"})
}

// WithMetrics registers runtime metrics once for all module pool generations.
// The registerer must belong to a single runtime.
func WithMetrics(reg prometheus.Registerer) Option {
	return func(r *Runtime) {
		reg.MustRegister(r.poolBorrowDuration)
	}
}
