package gateway

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"math/big"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
)

// crlFetcher is satisfied by *vaultpki.Client — kept as a small interface
// rather than importing vaultpki directly, matching the interface-based
// decoupling internal/engine/adminapi already uses for its own optional
// dependencies.
type crlFetcher interface {
	CRL(ctx context.Context) ([]byte, error)
}

// crlRevocationChecker implements RevocationChecker (mtls.go) by polling
// a CRL fetcher on an interval and checking a verified cert's serial
// against the most recently fetched list — admin-gateway-design.md §4:
// bounded staleness, not a live per-request check against the PKI backend.
type crlRevocationChecker struct {
	fetch crlFetcher

	mu      sync.RWMutex
	revoked map[string]struct{}
}

func newCRLRevocationChecker(ctx context.Context, fetch crlFetcher, interval time.Duration) *crlRevocationChecker {
	c := &crlRevocationChecker{fetch: fetch, revoked: map[string]struct{}{}}
	c.refresh(ctx)
	go c.pollLoop(ctx, interval)
	return c
}

func (c *crlRevocationChecker) pollLoop(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.refresh(ctx)
		}
	}
}

func (c *crlRevocationChecker) refresh(ctx context.Context) {
	pemBytes, err := c.fetch.CRL(ctx)
	if err != nil {
		log.Warn().Err(err).Msg("gateway: fetch CRL failed, keeping previous revocation list")
		return
	}

	block, _ := pem.Decode(pemBytes)
	if block == nil {
		log.Warn().Msg("gateway: CRL response was not valid PEM, keeping previous revocation list")
		return
	}
	crl, err := x509.ParseRevocationList(block.Bytes)
	if err != nil {
		log.Warn().Err(err).Msg("gateway: parse CRL failed, keeping previous revocation list")
		return
	}

	revoked := make(map[string]struct{}, len(crl.RevokedCertificateEntries))
	for _, entry := range crl.RevokedCertificateEntries {
		revoked[serialKey(entry.SerialNumber)] = struct{}{}
	}

	c.mu.Lock()
	c.revoked = revoked
	c.mu.Unlock()
}

func (c *crlRevocationChecker) IsRevoked(cert *x509.Certificate) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	_, ok := c.revoked[serialKey(cert.SerialNumber)]
	return ok
}

func serialKey(n *big.Int) string {
	return fmt.Sprintf("%x", n)
}
