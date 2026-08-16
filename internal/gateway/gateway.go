package gateway

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sync/atomic"

	"github.com/djangbahevans/goerp/internal/engine/secrets"
	"github.com/rs/zerolog/log"
)

type Gateway struct {
	cfg *Config

	server         *http.Server
	secretsBackend secrets.Backend

	adminToken string

	readiness atomic.Bool
}

func NewServer(cfg *Config) (*Gateway, error) {
	ctx := context.Background()

	secretsBackend, err := secrets.New(cfg.SecretsBackend)
	if err != nil {
		return nil, fmt.Errorf("create secrets backend: %w", err)
	}

	adminToken, err := secretsBackend.Get(ctx, "GOERP_ADMIN_TOKEN")
	if err != nil {
		return nil, fmt.Errorf("load admin token: %w", err)
	}

	upstreamURL, err := url.Parse(cfg.Upstream)
	if err != nil {
		return nil, fmt.Errorf("parse upstream url: %w", err)
	}

	// nil RevocationChecker until goerp#543 lands.
	tlsConfig, err := newMTLS(*cfg, nil)
	if err != nil {
		return nil, fmt.Errorf("build TLS config: %w", err)
	}

	proxy := httputil.NewSingleHostReverseProxy(upstreamURL)

	// Token check first, then identity, then proxy.
	var handler http.Handler = proxy
	handler = identityMiddleware(handler)
	handler = bearerTokenMiddleware(adminToken)(handler)

	server := &http.Server{
		Addr:      cfg.Addr.String(),
		Handler:   handler,
		TLSConfig: tlsConfig,
	}

	g := &Gateway{
		cfg:            cfg,
		secretsBackend: secretsBackend,
		server:         server,
		adminToken:     adminToken,
	}

	return g, nil
}

func (g *Gateway) Start(ctx context.Context) error {
	go func() {
		if err := g.server.ListenAndServeTLS(g.cfg.TLSCertFile, g.cfg.TLSKeyFile); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error().Err(err).Msg("admin gateway http server error")
		}
	}()

	g.readiness.Store(true)

	return nil
}

func (g *Gateway) Shutdown(ctx context.Context) error {
	log.Info().Msg("admin gateway shutdown initiated")

	g.readiness.Store(false)

	return g.server.Shutdown(ctx)
}
