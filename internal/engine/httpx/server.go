// Package httpx is the engine's HTTP server: the built-in `/_health` and
// `/_ready` endpoints (engine-internals.md §11's "Health endpoint"/
// "Readiness endpoint"), plus the app router other routes eventually mount
// on. It takes its own small Config rather than the engine's full
// *config.Config, and health/readiness checks are injected as closures
// (HealthFn, readyFn) so this package never needs to import database/sql, redis,
// or anything else it's merely reporting on.
package httpx

import (
	"context"
	"net/http"
	"time"

	"github.com/rs/zerolog/log"
)

type Server struct {
	cfg       *Config
	router    *http.ServeMux
	appRouter *http.ServeMux
	http      *http.Server
	readyFn   func(context.Context) error
	healthFn  HealthFn
	modulesFn ModulesFn
}

type Config struct {
	ListenAddr string
}

func NewServer(cfg *Config, readyFunc func(context.Context) error) *Server {
	r := http.NewServeMux()

	// TODO: Add middleware here

	app := http.NewServeMux()
	s := &Server{cfg: cfg, router: r, appRouter: app, readyFn: readyFunc}

	r.HandleFunc("GET /_health", s.handleHealth)
	r.HandleFunc("GET /_ready", s.handleReady)

	r.Handle("/", app)

	srv := &http.Server{
		Addr:         cfg.ListenAddr,
		Handler:      r,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second,
	}
	s.http = srv

	return s
}

func (s *Server) SetHealthFn(fn HealthFn) {
	s.healthFn = fn
}

func (s *Server) SetModulesFn(fn ModulesFn) {
	s.modulesFn = fn
}

func (s *Server) Start() error {
	log.Info().Str("addr", s.cfg.ListenAddr).Msg("http server listening")
	return s.http.ListenAndServe()
}

func (s *Server) Shutdown(ctx context.Context) error {
	return s.http.Shutdown(ctx)
}
