package wasm

import (
	"bytes"
	"cmp"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"slices"
	"strings"
	"time"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"golang.org/x/net/http/httpguts"
)

const (
	maxHTTPRequestBytes  = 5 << 20
	maxHTTPResponseBytes = 10 << 20
	maxHTTPRedirects     = 5
	defaultHTTPTimeoutMs = 10000
)

type httpFetcher struct {
	lookupIP func(context.Context, string, string) ([]netip.Addr, error)
	dial     func(context.Context, string, string) (net.Conn, error)
	roots    *x509.CertPool
}

func newHTTPFetcher() *httpFetcher {
	return &httpFetcher{
		lookupIP: net.DefaultResolver.LookupNetIP,
		dial:     (&net.Dialer{}).DialContext,
	}
}

func (f *httpFetcher) fetch(ctx context.Context, mc *ModuleContext, input abiv1.HTTPFetchInput) (abiv1.HTTPFetchOutput, *abiv1.HostError) {
	if len(input.Body) > maxHTTPRequestBytes {
		return abiv1.HTTPFetchOutput{}, httpError(abiv1.ErrCodeHTTPRequestTooLarge, "request body exceeds 5 MiB", false)
	}

	method := cmp.Or(input.Method, http.MethodGet)
	if !validHTTPMethod(method) || input.TimeoutMs < 0 || int64(input.TimeoutMs) > int64((1<<63-1)/time.Millisecond) {
		return abiv1.HTTPFetchOutput{}, httpError(abiv1.ErrCodeHTTPInvalidRequest, "invalid method or timeout_ms", false)
	}

	ctx, cancel := context.WithTimeout(ctx, time.Duration(cmp.Or(input.TimeoutMs, defaultHTTPTimeoutMs))*time.Millisecond)
	defer cancel()

	start := time.Now()

	u, hostErr := parseHTTPURL(input.URL)
	if hostErr != nil {
		return abiv1.HTTPFetchOutput{}, hostErr
	}

	headers, hostErr := outboundHTTPHeaders(input.Headers, mc.ModuleName)
	if hostErr != nil {
		return abiv1.HTTPFetchOutput{}, hostErr
	}

	body := input.Body
	for hop := 0; ; hop++ {
		if hostErr := validateHTTPAllowlist(u.Hostname(), mc.snapshot.HTTPAllowlist); hostErr != nil {
			return abiv1.HTTPFetchOutput{}, hostErr
		}

		ip, hostErr := f.resolveIP(ctx, u.Hostname())
		if hostErr != nil {
			return abiv1.HTTPFetchOutput{}, hostErr
		}

		resp, hostErr := f.request(ctx, mc, u, ip, method, headers, body)
		if hostErr != nil {
			return abiv1.HTTPFetchOutput{}, hostErr
		}

		if isHTTPRedirect(resp.StatusCode) && resp.Header.Get("Location") != "" && (input.FollowRedirects == nil || *input.FollowRedirects) {
			_ = resp.Body.Close()

			if hop >= maxHTTPRedirects {
				return abiv1.HTTPFetchOutput{}, httpError(abiv1.ErrCodeHTTPTooManyRedirects, "redirect limit of 5 exceeded", false)
			}

			ref, err := url.Parse(resp.Header.Get("Location"))
			if err != nil {
				return abiv1.HTTPFetchOutput{}, httpError(abiv1.ErrCodeHTTPInvalidURL, "invalid redirect URL", false)
			}

			next, hostErr := parseHTTPURL(u.ResolveReference(ref).String())
			if hostErr != nil {
				return abiv1.HTTPFetchOutput{}, hostErr
			}

			if normalizeHTTPHost(next.Hostname()) != normalizeHTTPHost(u.Hostname()) {
				headers.Del("Authorization")
				headers.Del("Cookie")
				headers.Del("Cookie2")
			}

			if (resp.StatusCode == http.StatusSeeOther && method != http.MethodHead) ||
				((resp.StatusCode == http.StatusMovedPermanently || resp.StatusCode == http.StatusFound) && method == http.MethodPost) {
				method, body = http.MethodGet, nil
				headers.Del("Content-Type")
				headers.Del("Content-Encoding")
			}

			u = next
			continue
		}

		data, err := io.ReadAll(io.LimitReader(resp.Body, maxHTTPResponseBytes+1))
		_ = resp.Body.Close()

		if len(data) > maxHTTPResponseBytes {
			return abiv1.HTTPFetchOutput{}, httpError(abiv1.ErrCodeHTTPResponseTooLarge, "response body exceeds 10 MiB", false)
		}

		if err != nil {
			return abiv1.HTTPFetchOutput{}, translateHTTPError(err)
		}

		responseHeaders := make(map[string]string, len(resp.Header))
		for key, values := range resp.Header {
			responseHeaders[key] = strings.Join(values, ", ")
		}

		return abiv1.HTTPFetchOutput{
			StatusCode: resp.StatusCode,
			Headers:    responseHeaders,
			Body:       data,
			DurationMs: float64(time.Since(start)) / float64(time.Millisecond),
		}, nil
	}
}

func parseHTTPURL(raw string) (*url.URL, *abiv1.HostError) {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || u.Opaque != "" || u.User != nil {
		return nil, httpError(abiv1.ErrCodeHTTPInvalidURL, "invalid URL or embedded userinfo", false)
	}

	if u.Scheme != "https" {
		return nil, httpError(abiv1.ErrCodeHTTPHTTPSRequired, "outbound requests require HTTPS", false)
	}

	if _, err := netip.ParseAddr(u.Hostname()); err == nil {
		return nil, httpError(abiv1.ErrCodeHTTPIPLiteralNotAllowed, "IP literal hosts are not allowed", false)
	}

	if (u.Port() != "" && u.Port() != "443") || strings.HasSuffix(u.Host, ":") {
		return nil, httpError(abiv1.ErrCodeHTTPPortNotAllowed, "outbound requests require port 443", false)
	}

	return u, nil
}

func normalizeHTTPHost(host string) string {
	return strings.ToLower(strings.TrimSuffix(host, "."))
}

func validateHTTPAllowlist(host string, allowlist []string) *abiv1.HostError {
	host = normalizeHTTPHost(host)
	for _, entry := range allowlist {
		u, err := url.Parse(entry)
		if err != nil || u.Scheme != "https" || u.User != nil || u.Hostname() == "" || (u.Port() != "" && u.Port() != "443") {
			continue
		}

		allowed := normalizeHTTPHost(u.Hostname())
		if base, wildcard := strings.CutPrefix(allowed, "*."); wildcard {
			if strings.HasSuffix(host, "."+base) {
				return nil
			}
		} else if host == allowed || strings.HasSuffix(host, "."+allowed) {
			return nil
		}
	}

	return httpError(abiv1.ErrCodeHTTPDomainNotAllowed, "target host is not in the module's http_allowlist", false)
}

func (f *httpFetcher) resolveIP(ctx context.Context, host string) (netip.Addr, *abiv1.HostError) {
	ips, err := f.lookupIP(ctx, "ip", host)
	if err != nil {
		if ctx.Err() != nil {
			return netip.Addr{}, translateHTTPError(ctx.Err())
		}

		return netip.Addr{}, httpError(abiv1.ErrCodeHTTPDNSFailed, "resolve outbound host", true)
	}

	if len(ips) == 0 {
		return netip.Addr{}, httpError(abiv1.ErrCodeHTTPDNSFailed, "outbound host has no IP addresses", true)
	}

	if slices.ContainsFunc(ips, blockedHTTPIP) {
		return netip.Addr{}, httpError(abiv1.ErrCodeHTTPPrivateIP, "outbound host resolves to a private or reserved address", false)
	}

	return ips[0], nil
}

var allocatedHTTPIPv6Range = netip.MustParsePrefix("2000::/3")

// Special-purpose ranges supplement IsGlobalUnicast and IsPrivate; IPv4-mapped
// IPv6 and address-translation ranges must not bypass the IPv4 checks.
var blockedHTTPRanges = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("192.88.99.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("::/96"),
	netip.MustParsePrefix("64:ff9b::/96"),
	netip.MustParsePrefix("64:ff9b:1::/48"),
	netip.MustParsePrefix("100::/64"),
	netip.MustParsePrefix("100:0:0:1::/64"),
	netip.MustParsePrefix("2001::/23"),
	netip.MustParsePrefix("2001:db8::/32"),
	netip.MustParsePrefix("2002::/16"),
	netip.MustParsePrefix("3ffe::/16"),
	netip.MustParsePrefix("3fff::/20"),
	netip.MustParsePrefix("5f00::/16"),
}

func blockedHTTPIP(ip netip.Addr) bool {
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.Is4In6() || ip.Zone() != "" {
		return true
	}

	// IsGlobalUnicast also accepts reserved IPv6 space, including site-local addresses.
	if ip.Is6() && !allocatedHTTPIPv6Range.Contains(ip) {
		return true
	}

	return slices.ContainsFunc(blockedHTTPRanges, func(prefix netip.Prefix) bool { return prefix.Contains(ip) })
}

func outboundHTTPHeaders(input map[string]string, moduleName string) (http.Header, *abiv1.HostError) {
	headers := make(http.Header, len(input)+1)
	for name, value := range input {
		if !httpguts.ValidHeaderFieldName(name) || !httpguts.ValidHeaderFieldValue(value) {
			return nil, httpError(abiv1.ErrCodeHTTPInvalidRequest, "invalid request header", false)
		}

		switch strings.ToLower(name) {
		case "host", "content-length", "transfer-encoding", "connection", "trailer", "upgrade", "proxy-authorization", "proxy-connection":
			continue
		}

		if strings.HasPrefix(strings.ToLower(name), "x-goerp-internal-") {
			continue
		}

		headers.Set(name, value)
	}

	if _, provided := headers["User-Agent"]; !provided {
		headers.Set("User-Agent", "GoERP/dev module/"+moduleName)
	}

	return headers, nil
}

func (f *httpFetcher) request(ctx context.Context, mc *ModuleContext, u *url.URL, ip netip.Addr, method string, headers http.Header, body []byte) (*http.Response, *abiv1.HostError) {
	// A fresh transport pins each hop to its validated address. Proxy and pooled
	// connections would bypass that pin, even if DNS validation were repeated.
	transport := &http.Transport{
		DialTLSContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			conn, err := f.dial(ctx, network, net.JoinHostPort(ip.String(), "443"))
			if err != nil {
				return nil, err
			}

			secured := tls.Client(conn, &tls.Config{MinVersion: tls.VersionTLS12, ServerName: u.Hostname(), RootCAs: f.roots})
			if err := secured.HandshakeContext(ctx); err != nil {
				_ = conn.Close()
				return nil, &httpTLSError{err}
			}

			return secured, nil
		},
		DisableKeepAlives:      true,
		MaxResponseHeaderBytes: 1 << 20,
	}

	defer transport.CloseIdleConnections()

	req, err := http.NewRequestWithContext(ctx, method, u.String(), bytes.NewReader(body))
	if err != nil {
		return nil, httpError(abiv1.ErrCodeHTTPInvalidURL, "construct outbound request", false)
	}

	req.Header = headers.Clone()
	start := time.Now()

	resp, err := transport.RoundTrip(req)
	status := 0
	var hostErr *abiv1.HostError

	if err != nil {
		hostErr = translateHTTPError(err)
	} else {
		status = resp.StatusCode
	}

	loggedURL := u.Clone()
	loggedURL.RawQuery, loggedURL.Fragment = "", ""
	logger := log.Ctx(ctx)
	if logger.GetLevel() == zerolog.Disabled {
		logger = &log.Logger
	}

	record := func() {
		event := logger.Info().Str("component", "outbound_http").Str("module", mc.ModuleName).
			Str("tenant_id", mc.TenantID).Str("trace_id", mc.TraceID).Str("url", loggedURL.String()).
			Str("method", method).Int("status", status).Dur("duration_ms", time.Since(start))
		if hostErr != nil {
			event = event.Str("error_code", hostErr.Code)
		}

		event.Msg("outbound HTTP request")
	}

	if hostErr != nil {
		record()
	} else {
		resp.Body = &httpAuditBody{ReadCloser: resp.Body, record: record}
	}

	return resp, hostErr
}

type httpAuditBody struct {
	io.ReadCloser
	record func()
}

func (b *httpAuditBody) Close() error {
	err := b.ReadCloser.Close()
	b.record()

	return err
}

func translateHTTPError(err error) *abiv1.HostError {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return httpError(abiv1.ErrCodeHTTPTimeout, "outbound request timed out or was canceled", true)
	}

	if timeout, ok := errors.AsType[net.Error](err); ok && timeout.Timeout() {
		return httpError(abiv1.ErrCodeHTTPTimeout, "outbound request timed out", true)
	}

	if _, ok := errors.AsType[*httpTLSError](err); ok {
		return httpError(abiv1.ErrCodeHTTPSSLError, "outbound TLS handshake failed", false)
	}

	return httpError(abiv1.ErrCodeHTTPConnectionRefused, "outbound connection failed", true)
}

type httpTLSError struct{ error }

func (e *httpTLSError) Unwrap() error { return e.error }

func httpError(code, message string, retry bool) *abiv1.HostError {
	return &abiv1.HostError{Code: code, Message: message, Retry: retry}
}

func isHTTPRedirect(status int) bool {
	switch status {
	case http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther,
		http.StatusTemporaryRedirect, http.StatusPermanentRedirect:
		return true
	default:
		return false
	}
}

func validHTTPMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodHead, http.MethodOptions:
		return true
	default:
		return false
	}
}
