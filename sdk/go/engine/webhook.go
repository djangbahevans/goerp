package engine

import (
	"fmt"
	"net/http"

	abi "github.com/djangbahevans/goerp/contract/abi/v1"
)

// WebhookVerifier authenticates one inbound provider webhook. secrets are the
// acceptable signing secrets, the current one first; a signature made with any
// of them is genuine. providerEventID, the provider's own event or reference
// ID, is required when valid is true. It is a pure function of its arguments:
// while it runs, only the crypto package's host calls are available.
type WebhookVerifier func(secrets [][]byte, headers http.Header, rawBody []byte) (valid bool, providerEventID string, err error)

var webhookVerifier WebhookVerifier

// RegisterWebhookVerifier registers the connector's webhook verifier, called
// in init(). A module has one ingress endpoint per tenant and so one
// verifier: it panics when fn is nil or a verifier is already registered, so
// the mistake fails when the module loads.
func RegisterWebhookVerifier(fn WebhookVerifier) {
	if fn == nil {
		panic("engine.RegisterWebhookVerifier: verifier is nil")
	}
	if webhookVerifier != nil {
		panic("engine.RegisterWebhookVerifier: a verifier is already registered")
	}
	webhookVerifier = fn
}

// DispatchWebhookVerify is what a connector's handle_webhook_verify export
// calls. A verifier that returns an error or panics produces a response whose
// Error is set, never a trap.
func DispatchWebhookVerify(ptr, length uint32) uint64 {
	var req abi.WebhookVerifyRequest
	if err := unmarshal(ReadMem(ptr, length), &req); err != nil {
		return writePacked(abi.WebhookVerifyResponse{Error: "decode request: " + err.Error()})
	}
	return writePacked(runWebhookVerifier(req))
}

func runWebhookVerifier(req abi.WebhookVerifyRequest) (resp abi.WebhookVerifyResponse) {
	if webhookVerifier == nil {
		return abi.WebhookVerifyResponse{Error: "no webhook verifier registered"}
	}
	defer func() {
		if r := recover(); r != nil {
			resp = abi.WebhookVerifyResponse{Error: fmt.Sprintf("verifier panicked: %v", r)}
		}
	}()

	valid, providerEventID, err := webhookVerifier(req.Secrets, canonicalHeader(req.Headers), req.Body)
	if err != nil {
		return abi.WebhookVerifyResponse{Error: err.Error()}
	}
	return abi.WebhookVerifyResponse{Valid: valid, ProviderEventID: providerEventID}
}

// canonicalHeader canonicalizes header names so the verifier's Header.Get
// finds a header however the caller cased it.
func canonicalHeader(headers map[string][]string) http.Header {
	out := make(http.Header, len(headers))
	for name, values := range headers {
		for _, value := range values {
			out.Add(name, value)
		}
	}
	return out
}
