package abi

type HTTPFetchInput struct {
	URL             string            `msgpack:"url"`
	Method          string            `msgpack:"method"`
	Headers         map[string]string `msgpack:"headers,omitempty"`
	Body            []byte            `msgpack:"body,omitempty"`
	TimeoutMs       int               `msgpack:"timeout_ms,omitempty"`
	FollowRedirects *bool             `msgpack:"follow_redirects,omitempty"`
}

type HTTPFetchOutput struct {
	StatusCode int               `msgpack:"status_code"`
	Headers    map[string]string `msgpack:"headers"`
	Body       []byte            `msgpack:"body"`
	DurationMs float64           `msgpack:"duration_ms"`
}

const (
	ErrCodeHTTPDomainNotAllowed    = "http.domain_not_allowed"
	ErrCodeHTTPInvalidURL          = "http.invalid_url"
	ErrCodeHTTPInvalidRequest      = "http.invalid_request"
	ErrCodeHTTPHTTPSRequired       = "http.https_required"
	ErrCodeHTTPIPLiteralNotAllowed = "http.ip_literal_not_allowed"
	ErrCodeHTTPPortNotAllowed      = "http.port_not_allowed"
	ErrCodeHTTPPrivateIP           = "http.private_ip"
	ErrCodeHTTPTimeout             = "http.timeout"
	ErrCodeHTTPConnectionRefused   = "http.connection_refused"
	ErrCodeHTTPDNSFailed           = "http.dns_failed"
	ErrCodeHTTPSSLError            = "http.ssl_error"
	ErrCodeHTTPTooManyRedirects    = "http.too_many_redirects"
	ErrCodeHTTPRequestTooLarge     = "http.request_too_large"
	ErrCodeHTTPResponseTooLarge    = "http.response_too_large"
)
