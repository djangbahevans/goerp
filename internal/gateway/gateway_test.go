package gateway

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// testCA is an ephemeral CA generated fresh per test, no fixtures checked in.
type testCA struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
}

func newTestCA(t *testing.T) *testCA {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate CA key: %v", err)
	}

	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test operator CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}

	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create CA certificate: %v", err)
	}

	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse CA certificate: %v", err)
	}

	return &testCA{cert: cert, key: key}
}

func (ca *testCA) certPEM() []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ca.cert.Raw})
}

// leaf issues a certificate signed by ca, as both raw PEM (for disk) and
// a parsed tls.Certificate (for direct client use).
func (ca *testCA) leaf(t *testing.T, cn string, server bool) (certPEM, keyPEM []byte, tlsCert tls.Certificate) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate leaf key: %v", err)
	}

	template := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
	}
	if server {
		template.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
		template.IPAddresses = []net.IP{net.ParseIP("127.0.0.1")}
	} else {
		template.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}
	}

	der, err := x509.CreateCertificate(rand.Reader, template, ca.cert, &key.PublicKey, ca.key)
	if err != nil {
		t.Fatalf("create leaf certificate: %v", err)
	}

	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("marshal leaf key: %v", err)
	}
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})

	tlsCert, err = tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatalf("build tls.Certificate: %v", err)
	}

	return certPEM, keyPEM, tlsCert
}

func writeFile(t *testing.T, dir, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return path
}

// requestRecorder captures the last request the fake upstream received.
type requestRecorder struct {
	method string
	path   string
	header http.Header
	body   string
}

// testGateway wires the real middleware chain in front of a fake upstream.
type testGateway struct {
	server   *httptest.Server
	ca       *testCA
	upstream *httptest.Server
	recorder *requestRecorder
}

func setupTestGateway(t *testing.T, adminToken string) *testGateway {
	t.Helper()

	rec := &requestRecorder{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		rec.method = r.Method
		rec.path = r.URL.Path
		rec.header = r.Header.Clone()
		rec.body = string(body)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	t.Cleanup(upstream.Close)

	ca := newTestCA(t)

	dir := t.TempDir()
	serverCertPEM, serverKeyPEM, _ := ca.leaf(t, "gateway", true)
	certPath := writeFile(t, dir, "server.crt", serverCertPEM)
	keyPath := writeFile(t, dir, "server.key", serverKeyPEM)
	caPath := writeFile(t, dir, "ca.crt", ca.certPEM())

	cfg := Config{
		Upstream:     upstream.URL,
		TLSCertFile:  certPath,
		TLSKeyFile:   keyPath,
		ClientCAFile: caPath,
	}

	tlsConfig, err := newMTLS(cfg, nil)
	if err != nil {
		t.Fatalf("newMTLS() error: %v", err)
	}

	proxy := newTestReverseProxy(t, cfg.Upstream)

	var handler http.Handler = proxy
	handler = identityMiddleware(handler)
	handler = bearerTokenMiddleware(adminToken)(handler)

	srv := httptest.NewUnstartedServer(handler)
	serverTLSCert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		t.Fatalf("load server keypair: %v", err)
	}
	tlsConfig.Certificates = []tls.Certificate{serverTLSCert}
	srv.TLS = tlsConfig
	// Silences expected handshake-error noise from the negative test cases.
	srv.Config.ErrorLog = log.New(io.Discard, "", 0)
	srv.StartTLS()
	t.Cleanup(srv.Close)

	return &testGateway{server: srv, ca: ca, upstream: upstream, recorder: rec}
}

func newTestReverseProxy(t *testing.T, upstreamURL string) http.Handler {
	t.Helper()
	u, err := url.Parse(upstreamURL)
	if err != nil {
		t.Fatalf("parse upstream url: %v", err)
	}
	return httputil.NewSingleHostReverseProxy(u)
}

// clientFor builds a client presenting certs to the gateway.
// InsecureSkipVerify is fine here — these tests exercise server-side mTLS
// enforcement, not client trust of the gateway's own server cert.
func clientFor(certs ...tls.Certificate) *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true,
				Certificates:       certs,
			},
		},
		Timeout: 5 * time.Second,
	}
}

func TestGateway_NoClientCert_HandshakeFails(t *testing.T) {
	gw := setupTestGateway(t, "correct-token")

	_, err := clientFor().Get(gw.server.URL)
	if err == nil {
		t.Fatal("expected TLS handshake failure with no client certificate, got nil error")
	}
}

func TestGateway_WrongCACert_HandshakeFails(t *testing.T) {
	gw := setupTestGateway(t, "correct-token")

	otherCA := newTestCA(t)
	_, _, wrongCert := otherCA.leaf(t, "impostor", false)

	_, err := clientFor(wrongCert).Get(gw.server.URL)
	if err == nil {
		t.Fatal("expected TLS handshake failure with a cert signed by a different CA, got nil error")
	}
}

func TestGateway_ValidCertMissingToken_ReturnsGatewayAuthFailed(t *testing.T) {
	gw := setupTestGateway(t, "correct-token")

	_, _, clientCert := gw.ca.leaf(t, "operator-a", false)

	resp, err := clientFor(clientCert).Get(gw.server.URL)
	if err != nil {
		t.Fatalf("Get() error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusUnauthorized)
	}

	var env authErrorEnvelope
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		t.Fatalf("decode error envelope: %v", err)
	}
	if env.Error.Code != gatewayAuthFailedCode {
		t.Errorf("error code = %q, want %q", env.Error.Code, gatewayAuthFailedCode)
	}
	if gw.recorder.method != "" {
		t.Error("upstream should never have received a request")
	}
}

func TestGateway_ValidCertWrongToken_ReturnsGatewayAuthFailed(t *testing.T) {
	gw := setupTestGateway(t, "correct-token")

	_, _, clientCert := gw.ca.leaf(t, "operator-a", false)

	req, _ := http.NewRequest(http.MethodGet, gw.server.URL, nil)
	req.Header.Set("Authorization", "Bearer wrong-token")

	resp, err := clientFor(clientCert).Do(req)
	if err != nil {
		t.Fatalf("Do() error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusUnauthorized)
	}
	if gw.recorder.method != "" {
		t.Error("upstream should never have received a request")
	}
}

func TestGateway_ValidCertValidToken_ForwardsAndSetsIdentityHeader(t *testing.T) {
	gw := setupTestGateway(t, "correct-token")

	_, _, clientCert := gw.ca.leaf(t, "kwame-operator", false)

	req, _ := http.NewRequest(http.MethodGet, gw.server.URL+"/admin/tenants", nil)
	req.Header.Set("Authorization", "Bearer correct-token")

	resp, err := clientFor(clientCert).Do(req)
	if err != nil {
		t.Fatalf("Do() error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	if gw.recorder.method != http.MethodGet {
		t.Errorf("upstream saw method %q, want GET", gw.recorder.method)
	}
	if gw.recorder.path != "/admin/tenants" {
		t.Errorf("upstream saw path %q, want /admin/tenants", gw.recorder.path)
	}
	if got := gw.recorder.header.Get(operatorIdentityHeader); got != "kwame-operator" {
		t.Errorf("%s = %q, want %q", operatorIdentityHeader, got, "kwame-operator")
	}
}

// Guards against a client forging another operator's audit identity.
func TestGateway_ClientSuppliedIdentityHeaderIsStripped(t *testing.T) {
	gw := setupTestGateway(t, "correct-token")

	_, _, clientCert := gw.ca.leaf(t, "real-operator", false)

	req, _ := http.NewRequest(http.MethodGet, gw.server.URL, nil)
	req.Header.Set("Authorization", "Bearer correct-token")
	req.Header.Set(operatorIdentityHeader, "forged-identity")

	resp, err := clientFor(clientCert).Do(req)
	if err != nil {
		t.Fatalf("Do() error: %v", err)
	}
	defer resp.Body.Close()

	if got := gw.recorder.header.Get(operatorIdentityHeader); got != "real-operator" {
		t.Errorf("%s = %q, want %q (client-supplied value must be overridden, not forwarded)", operatorIdentityHeader, got, "real-operator")
	}
}

func TestGateway_ForwardsBodyUnchanged(t *testing.T) {
	gw := setupTestGateway(t, "correct-token")

	_, _, clientCert := gw.ca.leaf(t, "operator-a", false)

	body := `{"slug":"acmecorp","name":"Acme Corp"}`
	req, _ := http.NewRequest(http.MethodPost, gw.server.URL+"/admin/tenants", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer correct-token")
	req.Header.Set("Content-Type", "application/json")

	resp, err := clientFor(clientCert).Do(req)
	if err != nil {
		t.Fatalf("Do() error: %v", err)
	}
	defer resp.Body.Close()

	if gw.recorder.body != body {
		t.Errorf("upstream saw body %q, want %q", gw.recorder.body, body)
	}
}

// Exercises the real NewServer -> Start -> Shutdown path, not just the
// middleware chain in isolation.
func TestNewServer_StartAndShutdown(t *testing.T) {
	t.Setenv("GOERP_ADMIN_TOKEN", "e2e-token")

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(upstream.Close)

	ca := newTestCA(t)
	dir := t.TempDir()
	serverCertPEM, serverKeyPEM, _ := ca.leaf(t, "gateway", true)
	certPath := writeFile(t, dir, "server.crt", serverCertPEM)
	keyPath := writeFile(t, dir, "server.key", serverKeyPEM)
	caPath := writeFile(t, dir, "ca.crt", ca.certPEM())

	addr := netip.MustParseAddrPort("127.0.0.1:0")
	cfg := &Config{
		Addr:           addr,
		Upstream:       upstream.URL,
		TLSCertFile:    certPath,
		TLSKeyFile:     keyPath,
		ClientCAFile:   caPath,
		SecretsBackend: "env",
	}

	gw, err := NewServer(cfg)
	if err != nil {
		t.Fatalf("NewServer() error: %v", err)
	}

	if gw.adminToken != "e2e-token" {
		t.Errorf("adminToken = %q, want %q (sourced via secrets backend)", gw.adminToken, "e2e-token")
	}

	ctx := context.Background()
	if err := gw.Start(ctx); err != nil {
		t.Fatalf("Start() error: %v", err)
	}

	shutdownCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := gw.Shutdown(shutdownCtx); err != nil {
		t.Errorf("Shutdown() error: %v", err)
	}
}
