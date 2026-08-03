package httpx

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/rs/zerolog/log"
)

type CheckResult struct {
	Status    string  `json:"status"`
	LatencyMs float64 `json:"latency_ms"`
	Error     string  `json:"error,omitempty"`
}

type HealthReport struct {
	Status        string                 `json:"status"`
	Version       string                 `json:"version"`
	UptimeSeconds int64                  `json:"uptime_seconds"`
	Checks        map[string]CheckResult `json:"checks"`
}

type ReadyReport struct {
	Ready         bool           `json:"ready"`
	Modules       ModulesReport  `json:"modules"`
	FailedModules []FailedModule `json:"failed_modules"`
}

type ModulesReport struct {
	Total  int `json:"total"`
	Ready  int `json:"ready"`
	Failed int `json:"failed"`
}

type FailedModule struct {
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

type HealthFn func(context.Context) HealthReport

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if s.healthFn == nil {
		if err := json.NewEncoder(w).Encode(HealthReport{
			Status:        "healthy",
			Version:       "dev",
			UptimeSeconds: 0,
			Checks:        map[string]CheckResult{},
		}); err != nil {
			log.Error().Err(err).Msg("encode health response")
		}
		return
	}

	report := s.healthFn(r.Context())

	if pg, ok := report.Checks["postgres_primary"]; ok && pg.Status != "ok" {
		w.WriteHeader(http.StatusServiceUnavailable)
	}

	if err := json.NewEncoder(w).Encode(report); err != nil {
		log.Error().Err(err).Msg("encode health response")
	}
}

func ProbeCheck(ctx context.Context, fn func(context.Context) error) CheckResult {
	start := time.Now()
	err := fn(ctx)

	latency := float64(time.Since(start).Milliseconds())
	if err != nil {
		return CheckResult{Status: "error", LatencyMs: latency, Error: err.Error()}
	}

	return CheckResult{Status: "ok", LatencyMs: latency}
}

func (s *Server) handleReady(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if s.readyFn != nil {
		if err := s.readyFn(r.Context()); err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			if encErr := json.NewEncoder(w).Encode(ReadyReport{
				Ready:         false,
				Modules:       ModulesReport{},
				FailedModules: []FailedModule{},
			}); encErr != nil {
				log.Error().Err(encErr).Msg("encode ready response")
			}
			return
		}
	}

	if err := json.NewEncoder(w).Encode(ReadyReport{
		Ready:         true,
		Modules:       ModulesReport{},
		FailedModules: []FailedModule{},
	}); err != nil {
		log.Error().Err(err).Msg("encode ready response")
	}
}
