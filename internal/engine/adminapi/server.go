// Package adminapi serves the authenticated operator API on a separate listener.
package adminapi

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/auditlog"
	"github.com/rs/zerolog/log"
)

var ErrEmptyAdminToken = errors.New("adminapi: AdminToken must not be empty")

type Config struct {
	ListenAddr string
	// AdminToken is the single shared GOERP_ADMIN_TOKEN, loaded by the
	// caller via the secrets backend. NewServer refuses an empty value
	// rather than silently minting an auth gate an empty
	// "Authorization: Bearer " header could satisfy.
	AdminToken string

	MaxBodyBytes     int64
	MaxConcurrent    int
	AuditStore       *auditlog.Store
	IdempotencyStore IdempotencyStore
}

type Server struct {
	cfg    *Config
	router *http.ServeMux
	top    *http.ServeMux
	http   *http.Server

	listener net.Listener
}

func NewServer(cfg *Config) (*Server, error) {
	if cfg.AdminToken == "" {
		return nil, ErrEmptyAdminToken
	}

	router := http.NewServeMux()

	authedHandler := idempotencyMiddleware(cfg.IdempotencyStore)(router)
	authedHandler = auditLogMiddleware(cfg.AuditStore)(authedHandler)
	auditedHandler := authedHandler
	authedHandler = adminAuthMiddleware(cfg.AdminToken)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, r.Pattern = router.Handler(r)
		auditedHandler.ServeHTTP(w, r)
	}))

	top := http.NewServeMux()
	top.Handle("/", authedHandler)

	var handler http.Handler = top
	handler = concurrencyLimitMiddleware(cfg.MaxConcurrent)(handler)
	handler = bodyCapMiddleware(cfg.MaxBodyBytes)(handler)

	s := &Server{
		cfg:    cfg,
		router: router,
		top:    top,
		http: &http.Server{
			Addr:         cfg.ListenAddr,
			Handler:      handler,
			ReadTimeout:  15 * time.Second,
			WriteTimeout: 30 * time.Second,
		},
	}

	return s, nil
}

// Router returns the authenticated mux admin routes register onto. Every
// handler added here sits behind adminAuthMiddleware and gets an
// admin_audit_log row per mutating request.
func (s *Server) Router() *http.ServeMux {
	return s.router
}

// UnauthenticatedRouter returns the mux for routes that skip
// adminAuthMiddleware/auditLogMiddleware; callers own their own auth.
func (s *Server) UnauthenticatedRouter() *http.ServeMux {
	return s.top
}

// Listen binds ListenAddr, so a taken port fails here, synchronously,
// rather than inside Serve's goroutine.
func (s *Server) Listen() error {
	ln, err := net.Listen("tcp", s.cfg.ListenAddr)
	if err != nil {
		return fmt.Errorf("bind admin http listener on %s: %w", s.cfg.ListenAddr, err)
	}
	s.listener = ln
	log.Info().Str("addr", ln.Addr().String()).Msg("admin http server listening")
	return nil
}

func (s *Server) Serve() error {
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
