package gateway

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"os"
)

func operatorIdentity(cert *x509.Certificate) string {
	return cert.Subject.CommonName
}

// RevocationChecker reports whether a verified client cert is revoked.
// nil until goerp#543 (cert issuance/revocation) lands.
type RevocationChecker interface {
	IsRevoked(cert *x509.Certificate) bool
}

// newMTLS builds the gateway's server-side tls.Config, verifying client
// certs against the operator CA at cfg.ClientCAFile. The gateway's own
// cert/key aren't loaded here — ListenAndServeTLS loads those directly.
func newMTLS(cfg Config, revocation RevocationChecker) (*tls.Config, error) {
	if _, err := os.Stat(cfg.TLSCertFile); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("TLS certificate file does not exist: %s", cfg.TLSCertFile)
		}
		return nil, fmt.Errorf("stat TLS certificate file: %w", err)
	}

	if _, err := os.Stat(cfg.TLSKeyFile); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("TLS key file does not exist: %s", cfg.TLSKeyFile)
		}
		return nil, fmt.Errorf("stat TLS key file: %w", err)
	}

	caPEM, err := os.ReadFile(cfg.ClientCAFile)
	if err != nil {
		return nil, fmt.Errorf("read client CA file: %w", err)
	}

	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		return nil, fmt.Errorf("client CA file %s contains no valid certificates", cfg.ClientCAFile)
	}

	tlsConfig := &tls.Config{
		ClientAuth: tls.RequireAndVerifyClientCert,
		ClientCAs:  pool,
		MinVersion: tls.VersionTLS12,
	}

	if revocation != nil {
		tlsConfig.VerifyPeerCertificate = func(rawCerts [][]byte, verifiedChains [][]*x509.Certificate) error {
			for _, chain := range verifiedChains {
				if len(chain) > 0 && revocation.IsRevoked(chain[0]) {
					return fmt.Errorf("client certificate revoked")
				}
			}
			return nil
		}
	}

	return tlsConfig, nil
}
