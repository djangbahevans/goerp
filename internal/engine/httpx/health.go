package httpx

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
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

// ModulesFn reports module-load state for GET /_ready (engine-internals.md
// §11). "Ready" means "Stage 3 load didn't fail," not StatusReady —
// instance warming (Stage 5) is separate, later scope.
type ModulesFn func() (ModulesReport, []FailedModule)

// writeJSON matches encoding/json v1's Encoder defaults, which
// json.MarshalWrite doesn't apply on its own: '<', '>', '&' escaped for
// safe HTML embedding, and U+2028/U+2029 escaped for safe JS embedding.
func writeJSON(w http.ResponseWriter, v any) error {
	return json.MarshalWrite(w, v, jsontext.EscapeForHTML(true), jsontext.EscapeForJS(true))
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if s.healthFn == nil {
		if err := writeJSON(w, HealthReport{
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

	if err := writeJSON(w, report); err != nil {
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

	modules, failedModules := ModulesReport{}, []FailedModule{}
	if s.modulesFn != nil {
		modules, failedModules = s.modulesFn()
		if failedModules == nil {
			failedModules = []FailedModule{}
		}
	}

	if s.readyFn != nil {
		if err := s.readyFn(r.Context()); err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			if encErr := writeJSON(w, ReadyReport{
				Ready:         false,
				Modules:       modules,
				FailedModules: failedModules,
			}); encErr != nil {
				log.Error().Err(encErr).Msg("encode ready response")
			}
			return
		}
	}

	if err := writeJSON(w, ReadyReport{
		Ready:         true,
		Modules:       modules,
		FailedModules: failedModules,
	}); err != nil {
		log.Error().Err(err).Msg("encode ready response")
	}
}
