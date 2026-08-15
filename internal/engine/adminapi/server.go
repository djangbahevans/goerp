// Package adminapi is the engine's admin HTTP server (engine-internals.md
// §11): a second listener, bound to whatever loopback address
// config.AdminAddr resolves to (loopback-ness is validated once, at config
// load time — this package trusts its ListenAddr and does not re-validate
// it), whose entire route tree sits behind adminAuthMiddleware plus a
// request body cap, a process-wide concurrency cap, and an audit-log
// write for every mutating request (§11 "Request limits" and "Audit
// logging"). It shares nothing with httpx's main-server middleware chain
// (no tenant resolution, no per-tenant rate limiting, no route auth) —
// the two servers' threat models differ. Actual /admin/* routes are
// registered onto Router() by tenant.go and later work.
package adminapi

import (
	"context"
	"errors"
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

	MaxBodyBytes  int64
	MaxConcurrent int
	AuditStore    *auditlog.Store
}

type Server struct {
	cfg    *Config
	router *http.ServeMux
	http   *http.Server
}

func NewServer(cfg *Config) (*Server, error) {
	if cfg.AdminToken == "" {
		return nil, ErrEmptyAdminToken
	}

	router := http.NewServeMux()

	// Innermost out: audit (only authenticated requests are attributable
	// to an operator) -> auth -> concurrency/body limits (guard the
	// process before spending any cycles on auth or routing).
	var handler http.Handler = router
	handler = auditLogMiddleware(cfg.AuditStore)(handler)
	handler = adminAuthMiddleware(cfg.AdminToken)(handler)
	handler = concurrencyLimitMiddleware(cfg.MaxConcurrent)(handler)
	handler = bodyCapMiddleware(cfg.MaxBodyBytes)(handler)

	s := &Server{
		cfg:    cfg,
		router: router,
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
// handler added here sits behind adminAuthMiddleware; there is currently
// no unauthenticated mount point (a future loopback-only-but-unauthenticated
// workflow-worker activity-dispatch route would need its own separate
// http.ServeMux wired in ahead of this one's auth wrapper).
func (s *Server) Router() *http.ServeMux {
	return s.router
}

func (s *Server) Start() error {
	log.Info().Str("addr", s.cfg.ListenAddr).Msg("admin http server listening")
	return s.http.ListenAndServe()
}

func (s *Server) Shutdown(ctx context.Context) error {
	return s.http.Shutdown(ctx)
}
