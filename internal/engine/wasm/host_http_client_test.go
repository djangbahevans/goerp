package wasm

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/json/v2"
	"errors"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/rs/zerolog"
)

func newHTTPTestContext(caps abi.CapabilitySet) *ModuleContext {
	return NewModuleContext("request", "testconnector", "user", "", nil, nil, "tenant", "httptest", "trace", caps, nil,
		ModuleSnapshot{HTTPAllowlist: []string{"https://example.com"}})
}

func newHTTPTestServer(t *testing.T, handler http.HandlerFunc) (*httpFetcher, *httptest.Server) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), DNSNames: []string{"example.com", "*.example.com", "sub.api.example.com"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}

	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}

	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}

	server := httptest.NewUnstartedServer(handler)
	server.TLS = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}, MinVersion: tls.VersionTLS12}
	server.StartTLS()
	t.Cleanup(server.Close)
	f := newHTTPFetcher()
	f.roots = x509.NewCertPool()
	f.roots.AddCert(cert)
	f.lookupIP = func(context.Context, string, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
	}

	f.dial = func(ctx context.Context, network, address string) (net.Conn, error) {
		if address != "93.184.216.34:443" {
			t.Errorf("dial address = %q, want the validated IP on port 443", address)
		}

		return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
	}

	return f, server
}

func requireHTTPError(t *testing.T, err *abiv1.HostError, code string) {
	t.Helper()
	if err == nil || err.Code != code {
		t.Fatalf("error = %v, want %s", err, code)
	}
}

func TestHTTPFetch_RejectsInvalidTargetsBeforeDNS(t *testing.T) {
	tests := []struct {
		url  string
		code string
	}{
		{"https://attacker.test/", abiv1.ErrCodeHTTPDomainNotAllowed},
		{"https://example.com.attacker.test/", abiv1.ErrCodeHTTPDomainNotAllowed},
		{"https://notexample.com/", abiv1.ErrCodeHTTPDomainNotAllowed},
		{"https://example.com@attacker.test/", abiv1.ErrCodeHTTPInvalidURL},
		{"https://user:pass@example.com/", abiv1.ErrCodeHTTPInvalidURL},
		{"http://example.com/", abiv1.ErrCodeHTTPHTTPSRequired},
		{"ftp://example.com/", abiv1.ErrCodeHTTPHTTPSRequired},
		{"https://example.com:8443/", abiv1.ErrCodeHTTPPortNotAllowed},
		{"https://example.com:/", abiv1.ErrCodeHTTPPortNotAllowed},
		{"https://127.0.0.1/", abiv1.ErrCodeHTTPIPLiteralNotAllowed},
		{"https://[::1]/", abiv1.ErrCodeHTTPIPLiteralNotAllowed},
		{"https://[::ffff:169.254.169.254]/", abiv1.ErrCodeHTTPIPLiteralNotAllowed},
		{"https://[fe80::1%25eth0]/", abiv1.ErrCodeHTTPIPLiteralNotAllowed},
		{"https://example.com/%zz", abiv1.ErrCodeHTTPInvalidURL},
		{"/relative", abiv1.ErrCodeHTTPInvalidURL},
	}

	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			f := newHTTPFetcher()
			f.lookupIP = func(context.Context, string, string) ([]netip.Addr, error) {
				t.Fatal("rejected target reached DNS")
				return nil, nil
			}

			_, err := f.fetch(t.Context(), newHTTPTestContext(abi.CapHTTPFetch), abiv1.HTTPFetchInput{URL: tt.url})
			requireHTTPError(t, err, tt.code)
		})
	}
}

func TestHTTPFetch_AllowlistHostBoundaries(t *testing.T) {
	for _, tt := range []struct {
		host      string
		allowlist []string
		allowed   bool
	}{
		{"EXAMPLE.COM.", []string{"https://example.com"}, true},
		{"sub.example.com", []string{"example.com"}, false},
		{"sub.example.com", []string{"https://*.example.com"}, true},
		{"example.com", []string{"https://*.example.com"}, false},
		{"example.com", []string{"http://example.com"}, false},
		{"example.com", []string{"https://example.com:8443"}, false},
		{"example.com", nil, false},
	} {
		if err := validateHTTPAllowlist(tt.host, tt.allowlist); (err == nil) != tt.allowed {
			t.Errorf("allowlist %v for %s: error = %v", tt.allowlist, tt.host, err)
		}
	}
}

func TestHTTPFetch_BlocksEveryPrivateOrReservedDNSAddress(t *testing.T) {
	blocked := []string{
		"0.0.0.0", "0.1.2.3", "10.1.2.3", "100.64.0.1", "127.0.0.1", "169.254.169.254",
		"172.16.0.1", "192.168.1.1", "192.0.0.1", "192.0.2.1", "192.88.99.1", "198.18.0.1",
		"198.51.100.1", "203.0.113.1", "224.0.0.1", "240.0.0.1", "255.255.255.255",
		"::", "::1", "::ffff:169.254.169.254", "::ffff:8.8.8.8", "::10.0.0.1", "fc00::1",
		"fe80::1", "fec0::1", "ff02::1", "64:ff9b::a00:1", "64:ff9b:1::1", "100::1", "100:0:0:1::1", "2001::1",
		"2001:db8::1", "2002:a00:1::1", "3ffe::1", "3fff::1", "4000::1", "5f00::1", "8000::1",
	}

	for _, address := range blocked {
		t.Run(address, func(t *testing.T) {
			f := newHTTPFetcher()
			f.lookupIP = func(context.Context, string, string) ([]netip.Addr, error) {
				return []netip.Addr{netip.MustParseAddr("8.8.8.8"), netip.MustParseAddr(address)}, nil
			}

			f.dial = func(context.Context, string, string) (net.Conn, error) {
				t.Error("dialed a hostname containing a blocked address")
				return nil, errors.New("unexpected dial")
			}

			_, err := f.fetch(t.Context(), newHTTPTestContext(abi.CapHTTPFetch), abiv1.HTTPFetchInput{URL: "https://example.com/"})
			requireHTTPError(t, err, abiv1.ErrCodeHTTPPrivateIP)
		})
	}

	for _, address := range []string{"8.8.8.8", "93.184.216.34", "2606:4700:4700::1111"} {
		if blockedHTTPIP(netip.MustParseAddr(address)) {
			t.Errorf("public address %s was rejected", address)
		}
	}
}

func TestHTTPFetch_PinsDNSAndPreservesTLSIdentity(t *testing.T) {
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:9")

	f, _ := newHTTPTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Host != "api.example.com" || r.TLS.ServerName != "api.example.com" {
			t.Errorf("Host = %q, SNI = %q", r.Host, r.TLS.ServerName)
		}

		for _, key := range []string{"X-Goerp-Internal-Tenant", "Proxy-Authorization", "Transfer-Encoding"} {
			if r.Header.Get(key) != "" {
				t.Errorf("reserved header %s was forwarded", key)
			}
		}

		if r.ContentLength != 7 || r.Header.Get("User-Agent") != "GoERP/dev module/testconnector" || r.Header.Get("Authorization") != "Bearer provider-secret" {
			t.Errorf("unexpected headers %v, length %d", r.Header, r.ContentLength)
		}

		body, _ := io.ReadAll(r.Body)
		w.Header().Set("X-Provider", "test")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write(body)
	})

	lookups := 0
	f.lookupIP = func(context.Context, string, string) ([]netip.Addr, error) {
		lookups++
		if lookups > 1 {
			return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
		}

		return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
	}

	input := abiv1.HTTPFetchInput{URL: "https://api.example.com/", Method: "POST", Body: []byte("payload"), Headers: map[string]string{
		"hOsT": "attacker.test", "Content-Length": "999", "X-GoErP-Internal-Tenant": "other",
		"Proxy-Authorization": "secret", "Transfer-Encoding": "chunked", "Authorization": "Bearer provider-secret",
	}}
	out, err := f.fetch(t.Context(), newHTTPTestContext(abi.CapHTTPFetch), input)
	if err != nil {
		t.Fatal(err)
	}

	if lookups != 1 || out.StatusCode != 201 || string(out.Body) != "payload" || out.Headers["X-Provider"] != "test" || out.DurationMs <= 0 {
		t.Fatalf("lookups = %d, output = %+v", lookups, out)
	}

	if input.Headers["hOsT"] != "attacker.test" {
		t.Error("fetch mutated the caller's headers")
	}
}

func TestHTTPFetch_RedirectSecurityAndMethods(t *testing.T) {
	for _, tt := range []struct {
		name       string
		location   string
		status     int
		follow     *bool
		method     string
		wantMethod string
		wantBody   string
		wantAuth   string
		wantCode   string
	}{
		{"relative", "/final", 307, nil, "POST", "POST", "payload", "secret", ""},
		{"same host", "https://api.example.com/final", 308, nil, "POST", "POST", "payload", "secret", ""},
		{"cross host", "https://other.example.com/final", 307, nil, "POST", "POST", "payload", "", ""},
		{"post 302", "/final", 302, nil, "POST", "GET", "", "secret", ""},
		{"put 303", "/final", 303, nil, "PUT", "GET", "", "secret", ""},
		{"head 303", "/final", 303, nil, "HEAD", "HEAD", "", "secret", ""},
		{"disabled", "https://attacker.test/final", 302, new(false), "GET", "", "", "", ""},
		{"not allowlisted", "https://attacker.test/final", 307, nil, "GET", "", "", "", abiv1.ErrCodeHTTPDomainNotAllowed},
		{"private DNS", "https://private.example.com/final", 307, nil, "GET", "", "", "", abiv1.ErrCodeHTTPPrivateIP},
		{"HTTP redirect", "http://api.example.com/final", 307, nil, "GET", "", "", "", abiv1.ErrCodeHTTPHTTPSRequired},
		{"IP redirect", "https://169.254.169.254/", 307, nil, "GET", "", "", "", abiv1.ErrCodeHTTPIPLiteralNotAllowed},
		{"userinfo redirect", "https://user:pass@api.example.com/final", 307, nil, "GET", "", "", "", abiv1.ErrCodeHTTPInvalidURL},
		{"bad redirect", "%zz", 307, nil, "GET", "", "", "", abiv1.ErrCodeHTTPInvalidURL},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var finalCalls atomic.Int64
			f, _ := newHTTPTestServer(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/start" {
					w.Header().Set("Location", tt.location)
					w.WriteHeader(tt.status)
					return
				}

				finalCalls.Add(1)
				body, _ := io.ReadAll(r.Body)
				if r.Method != tt.wantMethod || string(body) != tt.wantBody || r.Header.Get("Authorization") != tt.wantAuth || r.Header.Get("Cookie") != tt.wantAuth {
					t.Errorf("final request: method=%s body=%s headers=%v", r.Method, body, r.Header)
				}

				w.WriteHeader(200)
			})

			lookup := f.lookupIP
			f.lookupIP = func(ctx context.Context, network, host string) ([]netip.Addr, error) {
				if host == "private.example.com" {
					return []netip.Addr{netip.MustParseAddr("10.0.0.1")}, nil
				}

				return lookup(ctx, network, host)
			}

			body := []byte("payload")
			if tt.method == "GET" || tt.method == "HEAD" {
				body = nil
			}

			out, err := f.fetch(t.Context(), newHTTPTestContext(abi.CapHTTPFetch), abiv1.HTTPFetchInput{
				URL: "https://api.example.com/start", Method: tt.method, Body: body, FollowRedirects: tt.follow,
				Headers: map[string]string{"Authorization": "secret", "Cookie": "secret"},
			})

			if tt.wantCode != "" {
				requireHTTPError(t, err, tt.wantCode)
			} else if err != nil {
				t.Fatal(err)
			}

			if tt.wantMethod == "" && finalCalls.Load() != 0 {
				t.Error("redirect target was contacted")
			}

			if tt.wantMethod != "" && finalCalls.Load() != 1 {
				t.Errorf("final calls = %d", finalCalls.Load())
			}

			if tt.follow != nil && !*tt.follow && out.StatusCode != tt.status {
				t.Errorf("unfollowed status = %d", out.StatusCode)
			}
		})
	}
}

func TestHTTPFetch_RedirectLimitAndRepeatedDNSValidation(t *testing.T) {
	var requests atomic.Int64
	f, _ := newHTTPTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Location", "/loop")
		w.WriteHeader(302)
	})

	lookups := 0
	lookup := f.lookupIP
	f.lookupIP = func(ctx context.Context, network, host string) ([]netip.Addr, error) {
		lookups++
		return lookup(ctx, network, host)
	}

	_, err := f.fetch(t.Context(), newHTTPTestContext(abi.CapHTTPFetch), abiv1.HTTPFetchInput{URL: "https://example.com/loop"})
	requireHTTPError(t, err, abiv1.ErrCodeHTTPTooManyRedirects)
	if requests.Load() != 6 || lookups != 6 {
		t.Fatalf("requests = %d, lookups = %d, want initial request and five redirects", requests.Load(), lookups)
	}

	requests.Store(0)
	lookups = 0
	f.lookupIP = func(ctx context.Context, network, host string) ([]netip.Addr, error) {
		lookups++
		if lookups == 2 {
			return []netip.Addr{netip.MustParseAddr("10.0.0.1")}, nil
		}

		return lookup(ctx, network, host)
	}

	_, err = f.fetch(t.Context(), newHTTPTestContext(abi.CapHTTPFetch), abiv1.HTTPFetchInput{URL: "https://example.com/loop"})
	requireHTTPError(t, err, abiv1.ErrCodeHTTPPrivateIP)
	if requests.Load() != 1 || lookups != 2 {
		t.Fatalf("requests = %d, lookups = %d after DNS changed", requests.Load(), lookups)
	}
}

func TestHTTPFetch_SizeLimits(t *testing.T) {
	var requests atomic.Int64
	f, _ := newHTTPTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path == "/request" {
			n, _ := io.Copy(io.Discard, r.Body)
			if n != maxHTTPRequestBytes {
				t.Errorf("request bytes = %d", n)
			}

			return
		}

		size := maxHTTPResponseBytes
		if r.URL.Path == "/overflow" {
			size++
		}

		_, _ = io.Copy(w, io.LimitReader(infiniteHTTPTestReader{}, int64(size)))
	})

	mc := newHTTPTestContext(abi.CapHTTPFetch)
	_, err := f.fetch(t.Context(), mc, abiv1.HTTPFetchInput{URL: "https://example.com/request", Body: make([]byte, maxHTTPRequestBytes+1)})
	requireHTTPError(t, err, abiv1.ErrCodeHTTPRequestTooLarge)
	if requests.Load() != 0 {
		t.Fatal("oversized request was sent")
	}

	_, err = f.fetch(t.Context(), mc, abiv1.HTTPFetchInput{URL: "https://example.com/request", Method: "POST", Body: make([]byte, maxHTTPRequestBytes)})
	if err != nil {
		t.Fatal(err)
	}

	out, err := f.fetch(t.Context(), mc, abiv1.HTTPFetchInput{URL: "https://example.com/exact"})
	if err != nil || len(out.Body) != maxHTTPResponseBytes {
		t.Fatalf("exact response: bytes=%d, error=%v", len(out.Body), err)
	}

	_, err = f.fetch(t.Context(), mc, abiv1.HTTPFetchInput{URL: "https://example.com/overflow"})
	requireHTTPError(t, err, abiv1.ErrCodeHTTPResponseTooLarge)
}

type infiniteHTTPTestReader struct{}

func (infiniteHTTPTestReader) Read(p []byte) (int, error) {
	clear(p)
	return len(p), nil
}

func TestHTTPFetch_TimeoutCoversResponseBodyAndDNS(t *testing.T) {
	f, _ := newHTTPTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	})

	_, err := f.fetch(t.Context(), newHTTPTestContext(abi.CapHTTPFetch), abiv1.HTTPFetchInput{URL: "https://example.com/", TimeoutMs: 50})
	requireHTTPError(t, err, abiv1.ErrCodeHTTPTimeout)
	f.lookupIP = func(ctx context.Context, _, _ string) ([]netip.Addr, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}

	_, err = f.fetch(t.Context(), newHTTPTestContext(abi.CapHTTPFetch), abiv1.HTTPFetchInput{URL: "https://example.com/", TimeoutMs: 1})
	requireHTTPError(t, err, abiv1.ErrCodeHTTPTimeout)
}

func TestHTTPFetch_TLSVerificationCannotBeDisabled(t *testing.T) {
	f, _ := newHTTPTestServer(t, func(w http.ResponseWriter, r *http.Request) { t.Error("unverified TLS reached HTTP") })
	f.roots = x509.NewCertPool()
	_, err := f.fetch(t.Context(), newHTTPTestContext(abi.CapHTTPFetch), abiv1.HTTPFetchInput{URL: "https://example.com/"})
	requireHTTPError(t, err, abiv1.ErrCodeHTTPSSLError)
}

func TestHTTPFetch_TLSRejectsWrongHostnameAndOldProtocol(t *testing.T) {
	t.Run("wrong hostname", func(t *testing.T) {
		f, _ := newHTTPTestServer(t, func(http.ResponseWriter, *http.Request) { t.Error("wrong certificate reached HTTP") })
		_, err := f.fetch(t.Context(), newHTTPTestContext(abi.CapHTTPFetch), abiv1.HTTPFetchInput{URL: "https://deep.other.example.com/"})
		requireHTTPError(t, err, abiv1.ErrCodeHTTPSSLError)
	})

	t.Run("TLS before 1.2", func(t *testing.T) {
		f, server := newHTTPTestServer(t, func(http.ResponseWriter, *http.Request) { t.Error("old TLS reached HTTP") })
		server.TLS.MinVersion = tls.VersionTLS10
		server.TLS.MaxVersion = tls.VersionTLS11

		_, err := f.fetch(t.Context(), newHTTPTestContext(abi.CapHTTPFetch), abiv1.HTTPFetchInput{URL: "https://example.com/"})
		requireHTTPError(t, err, abiv1.ErrCodeHTTPSSLError)
	})
}

func TestHTTPFetch_DNSAndConnectionErrors(t *testing.T) {
	for _, empty := range []bool{false, true} {
		f := newHTTPFetcher()
		f.lookupIP = func(context.Context, string, string) ([]netip.Addr, error) {
			if empty {
				return nil, nil
			}

			return nil, &net.DNSError{Err: "not found", Name: "example.com", IsNotFound: true}
		}

		_, err := f.fetch(t.Context(), newHTTPTestContext(abi.CapHTTPFetch), abiv1.HTTPFetchInput{URL: "https://example.com/"})
		requireHTTPError(t, err, abiv1.ErrCodeHTTPDNSFailed)
	}

	f, _ := newHTTPTestServer(t, func(http.ResponseWriter, *http.Request) {})
	f.dial = func(context.Context, string, string) (net.Conn, error) { return nil, errors.New("connection refused") }
	_, err := f.fetch(t.Context(), newHTTPTestContext(abi.CapHTTPFetch), abiv1.HTTPFetchInput{URL: "https://example.com/"})
	requireHTTPError(t, err, abiv1.ErrCodeHTTPConnectionRefused)
}

func TestHTTPFetch_InvalidOptionsAndHeaderInjection(t *testing.T) {
	for _, input := range []abiv1.HTTPFetchInput{
		{Method: "CONNECT"}, {TimeoutMs: -1}, {TimeoutMs: int(^uint(0) >> 1)},
		{Headers: map[string]string{"Bad Header": "value"}}, {Headers: map[string]string{"X-Header": "value\r\nInjected: yes"}},
	} {
		input.URL = "https://example.com/"
		f := newHTTPFetcher()
		f.lookupIP = func(context.Context, string, string) ([]netip.Addr, error) {
			t.Fatal("invalid input reached DNS")
			return nil, nil
		}

		_, err := f.fetch(t.Context(), newHTTPTestContext(abi.CapHTTPFetch), input)
		requireHTTPError(t, err, abiv1.ErrCodeHTTPInvalidRequest)
	}
}

func TestHTTPFetch_CrossHostCredentialsStayRemovedOnReturn(t *testing.T) {
	var mu sync.Mutex
	var hosts []string
	f, _ := newHTTPTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hosts = append(hosts, r.Host)
		mu.Unlock()
		switch r.URL.Path {
		case "/start":
			w.Header().Set("Location", "https://other.example.com/return")
			w.WriteHeader(307)
		case "/return":
			w.Header().Set("Location", "https://api.example.com/final")
			w.WriteHeader(307)
		default:
			if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
				t.Error("credentials restored after crossing hosts")
			}
		}
	})

	_, err := f.fetch(t.Context(), newHTTPTestContext(abi.CapHTTPFetch), abiv1.HTTPFetchInput{
		URL: "https://api.example.com/start", Headers: map[string]string{"Authorization": "secret", "Cookie": "secret"},
	})

	if err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	defer mu.Unlock()
	if !slices.Equal(hosts, []string{"api.example.com", "other.example.com", "api.example.com"}) {
		t.Errorf("hosts = %v", hosts)
	}
}

func TestHTTPFetch_UserAgentOverride(t *testing.T) {
	headers, err := outboundHTTPHeaders(map[string]string{"user-agent": "Custom/1"}, "connector")
	if err != nil || headers.Get("User-Agent") != "Custom/1" {
		t.Fatalf("headers=%v, error=%v", headers, err)
	}
}

func TestHTTPFetch_AuditsEachHopWithoutQueryCredentials(t *testing.T) {
	f, _ := newHTTPTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/start" {
			w.Header().Set("Location", "/final?token=redirect-secret")
			w.WriteHeader(307)
		}
	})

	var output bytes.Buffer
	logger := zerolog.New(&output)
	ctx := logger.WithContext(t.Context())
	_, err := f.fetch(ctx, newHTTPTestContext(abi.CapHTTPFetch), abiv1.HTTPFetchInput{
		URL:     "https://api.example.com/start?key=provider-secret#fragment-secret",
		Headers: map[string]string{"Authorization": "Bearer header-secret"},
	})
	if err != nil {
		t.Fatal(err)
	}

	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("audit lines = %d, want one per hop: %s", len(lines), output.String())
	}

	for i, line := range lines {
		var event map[string]any
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatal(err)
		}

		wantURL := []string{"https://api.example.com/start", "https://api.example.com/final"}[i]
		if event["url"] != wantURL || event["module"] != "testconnector" || event["tenant_id"] != "tenant" || event["trace_id"] != "trace" || event["method"] != "GET" || event["status"] == nil || event["duration_ms"] == nil {
			t.Errorf("audit event = %v", event)
		}
	}

	if strings.Contains(output.String(), "secret") {
		t.Errorf("credentials in audit log: %s", output.String())
	}
}

func TestHTTPFetch_ResponseLimitAppliesAfterDecompression(t *testing.T) {
	f, _ := newHTTPTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Encoding", "gzip")
		compressed := gzip.NewWriter(w)
		_, _ = io.Copy(compressed, io.LimitReader(infiniteHTTPTestReader{}, maxHTTPResponseBytes+1))
		_ = compressed.Close()
	})

	_, err := f.fetch(t.Context(), newHTTPTestContext(abi.CapHTTPFetch), abiv1.HTTPFetchInput{URL: "https://example.com/"})
	requireHTTPError(t, err, abiv1.ErrCodeHTTPResponseTooLarge)
}

func TestHTTPFetch_DefaultTimeoutAndParentCancellation(t *testing.T) {
	f := newHTTPFetcher()
	f.lookupIP = func(ctx context.Context, _, _ string) ([]netip.Addr, error) {
		deadline, ok := ctx.Deadline()
		remaining := time.Until(deadline)
		if !ok || remaining > 10*time.Second || remaining < 9*time.Second {
			t.Errorf("default deadline = %v (present=%v)", remaining, ok)
		}

		return nil, errors.New("stop before dial")
	}
	_, err := f.fetch(t.Context(), newHTTPTestContext(abi.CapHTTPFetch), abiv1.HTTPFetchInput{URL: "https://example.com/"})
	requireHTTPError(t, err, abiv1.ErrCodeHTTPDNSFailed)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	f.lookupIP = func(ctx context.Context, _, _ string) ([]netip.Addr, error) { return nil, ctx.Err() }
	_, err = f.fetch(ctx, newHTTPTestContext(abi.CapHTTPFetch), abiv1.HTTPFetchInput{URL: "https://example.com/", TimeoutMs: 30000})
	requireHTTPError(t, err, abiv1.ErrCodeHTTPTimeout)
}
