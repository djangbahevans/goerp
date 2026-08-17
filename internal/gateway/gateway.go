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
	"github.com/djangbahevans/goerp/internal/engine/vaultpki"
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

	// RevocationChecker only makes sense with a real PKI backend behind
	// it; stays nil for any other GOERP_SECRETS_BACKEND (goerp#181's own
	// nil-safe-until-wired pattern, same as mtls.go's doc comment).
	var revocation RevocationChecker
	if cfg.SecretsBackend == "vault" {
		pki, err := vaultpki.New()
		if err != nil {
			log.Warn().Err(err).Msg("admin gateway: could not create vault pki client, certificate revocation checking disabled")
		} else {
			revocation = newCRLRevocationChecker(context.Background(), pki, cfg.RevocationPollInterval)
		}
	}

	tlsConfig, err := newMTLS(*cfg, revocation)
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
