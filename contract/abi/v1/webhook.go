package abi

// WebhookVerifyRequest is the request of the handle_webhook_verify export.
// Secrets are the acceptable signing secrets in order, the current one first.
// Headers are keyed by canonical MIME header name, as net/http.Header.
type WebhookVerifyRequest struct {
	Secrets [][]byte            `msgpack:"secrets"`
	Headers map[string][]string `msgpack:"headers"`
	Body    []byte              `msgpack:"body"`
}

// WebhookVerifyResponse is the response of handle_webhook_verify. Error is
// set when the verifier returned an error, panicked, or none was registered.
// ProviderEventID is required and non-empty when Valid is true.
type WebhookVerifyResponse struct {
	Valid           bool   `msgpack:"valid"`
	ProviderEventID string `msgpack:"provider_event_id,omitempty"`
	Error           string `msgpack:"error,omitempty"`
}
