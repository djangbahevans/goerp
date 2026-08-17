// Package httpx is the engine's HTTP server: the built-in `/_health` and
// `/_ready` endpoints (engine-internals.md §11's "Health endpoint"/
// "Readiness endpoint"), plus the http.Server lifecycle (timeouts, TLS,
// start/shutdown) around whatever single Handler the caller supplies. It
// takes its own small Config rather than the engine's full *config.Config,
// and health/readiness checks are injected as closures (HealthFn,
// readyFn) so this package never needs to import database/sql, redis, or
// anything else it's merely reporting on. Routing decisions — including
// how /_health and /_ready reach HealthHandler/ReadyHandler below — belong
// to the caller (engine.go), which has the module registry/route table
// this package deliberately doesn't import.
package httpx

import (
	"context"
	"net/http"
	"time"

	"github.com/rs/zerolog/log"
)

type Server struct {
	cfg       *Config
	http      *http.Server
	readyFn   func(context.Context) error
	healthFn  HealthFn
	modulesFn ModulesFn
}

type Config struct {
	ListenAddr              string
	ReadTimeout             time.Duration
	ReadHeaderTimeout       time.Duration
	WriteTimeout            time.Duration
	IdleTimeout             time.Duration
	MaxHeaderBytes          int
	TLSCertFile, TLSKeyFile string
}

// NewServer builds the server around handler — the engine's single
// dispatch handler, covering built-ins and module routes alike (no
// separate router exists anywhere in the request path).
func NewServer(cfg *Config, handler http.Handler, readyFunc func(context.Context) error) *Server {
	s := &Server{cfg: cfg, readyFn: readyFunc}

	s.http = &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           handler,
		ReadTimeout:       cfg.ReadTimeout,
		ReadHeaderTimeout: cfg.ReadHeaderTimeout,
		WriteTimeout:      cfg.WriteTimeout,
		IdleTimeout:       cfg.IdleTimeout,
		MaxHeaderBytes:    cfg.MaxHeaderBytes,
	}

	return s
}

// SetHandler swaps the server's Handler after construction — needed
// because the engine's real dispatch handler depends on the module
// registry, which isn't built until after the server itself is
// constructed (so SetHealthFn/SetModulesFn have somewhere to attach).
func (s *Server) SetHandler(handler http.Handler) {
	s.http.Handler = handler
}

func (s *Server) SetHealthFn(fn HealthFn) {
	s.healthFn = fn
}

func (s *Server) SetModulesFn(fn ModulesFn) {
	s.modulesFn = fn
}

// HealthHandler and ReadyHandler let the caller register /_health/_ready
// into its own route table as the built-ins' real dispatch targets —
// same handlers as before, just no longer self-registered on a private
// mux.
func (s *Server) HealthHandler() http.HandlerFunc { return s.handleHealth }
func (s *Server) ReadyHandler() http.HandlerFunc  { return s.handleReady }

// Start listens and serves, using TLS when both TLSCertFile and
// TLSKeyFile are configured; otherwise TLS is assumed to terminate
// upstream (a load balancer or ingress) and this serves plain HTTP.
func (s *Server) Start() error {
	log.Info().Str("addr", s.cfg.ListenAddr).Msg("http server listening")
	if s.cfg.TLSCertFile != "" && s.cfg.TLSKeyFile != "" {
		return s.http.ListenAndServeTLS(s.cfg.TLSCertFile, s.cfg.TLSKeyFile)
	}
	return s.http.ListenAndServe()
}

func (s *Server) Shutdown(ctx context.Context) error {
	return s.http.Shutdown(ctx)
}
