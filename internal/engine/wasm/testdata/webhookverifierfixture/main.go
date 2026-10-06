// Command webhookverifierfixture is a real Go module compiled to wasip1 WASM
// for internal/engine/wasm's webhook verifier tests. It registers one verifier
// through the real sdk/go/engine whose behavior the X-Mode header selects, so
// each test drives a different outcome.
//
// Must be built with:
//
//	GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o webhookverifierfixture.wasm .
package main

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/djangbahevans/goerp/sdk/go/config"
	"github.com/djangbahevans/goerp/sdk/go/crypto"
	"github.com/djangbahevans/goerp/sdk/go/engine"
	"github.com/djangbahevans/goerp/sdk/go/jobs"
)

var token = config.String("token", "", config.Label("Token"))

func init() {
	engine.RegisterWebhookVerifier(verify)
}

func verify(secrets [][]byte, headers http.Header, rawBody []byte) (bool, string, error) {
	eventID := headers.Get("X-Event-Id")
	switch headers.Get("X-Mode") {
	case "valid":
		return true, eventID, nil
	case "invalid":
		return false, "", nil
	case "error":
		return false, "", errors.New("malformed delivery")
	case "panic":
		panic("verifier bug")
	case "no-id":
		return true, "", nil
	case "spin":
		for {
		}
	case "echo":
		return true, fmt.Sprintf("%d secrets, body %s", len(secrets), rawBody), nil
	case "hmac":
		for _, secret := range secrets {
			if crypto.VerifyHMACSHA256Hex(secret, rawBody, headers.Get("X-Signature")) {
				return true, eventID, nil
			}
		}
		return false, "", nil
	case "config-set":
		return false, "", token.Set("x")
	case "enqueue":
		_, err := jobs.EnqueueProvider("sms_provider", "sms_send", map[string]string{})
		return false, "", err
	}
	return false, "", errors.New("unknown mode")
}

//go:wasmexport handle_webhook_verify
func handleWebhookVerify(ptr, length uint32) uint64 {
	return engine.DispatchWebhookVerify(ptr, length)
}

//go:wasmexport allocate
func allocate(size uint32) uint32 {
	return engine.Allocate(size)
}

//go:wasmexport deallocate
func deallocate(ptr, size uint32) {
	engine.Deallocate(ptr, size)
}

func main() {}
