// Package httpx provides health/readiness handlers and HTTP listener lifecycle. Callers
// supply routing and dependency probes without coupling this package to their clients.
package httpx

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/rs/zerolog/log"
)

type Server struct {
	cfg       *Config
	http      *http.Server
	listener  net.Listener
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

// HealthHandler and ReadyHandler expose handlers for registration in the caller's route
// table.
func (s *Server) HealthHandler() http.HandlerFunc { return s.handleHealth }
func (s *Server) ReadyHandler() http.HandlerFunc  { return s.handleReady }

// Listen binds ListenAddr, so a taken port fails here, synchronously,
// rather than inside Serve's goroutine.
func (s *Server) Listen() error {
	ln, err := net.Listen("tcp", s.cfg.ListenAddr)
	if err != nil {
		return fmt.Errorf("bind http listener on %s: %w", s.cfg.ListenAddr, err)
	}
	s.listener = ln
	log.Info().Str("addr", ln.Addr().String()).Msg("http server listening")
	return nil
}

// Serve serves on the listener Listen bound, using TLS when both
// TLSCertFile and TLSKeyFile are configured; otherwise TLS is assumed to
// terminate upstream (a load balancer or ingress) and this serves plain
// HTTP.
func (s *Server) Serve() error {
	if s.cfg.TLSCertFile != "" && s.cfg.TLSKeyFile != "" {
		return s.http.ServeTLS(s.listener, s.cfg.TLSCertFile, s.cfg.TLSKeyFile)
	}
	return s.http.Serve(s.listener)
}

// Close releases the listener Listen bound, for a startup that fails
// before Serve runs.
func (s *Server) Close() error {
	if s.listener == nil {
		return nil
	}
	return s.listener.Close()
}

func (s *Server) Shutdown(ctx context.Context) error {
	return s.http.Shutdown(ctx)
}
