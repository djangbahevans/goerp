// Command hmacverifierfixture is a real Go module compiled to wasip1 WASM for
// internal/engine/wasm's host.crypto.verify_hmac module-side test. It holds
// example Paystack and Stripe webhook verifiers built on sdk/go/crypto, each
// exported with a (ptr, size) request so the test can drive them with
// arbitrary signed payloads.
//
// Must be built with:
//
//	GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o hmacverifierfixture.wasm .
package main

import (
	"encoding/json/v2"
	"strconv"
	"strings"
	"time"

	"github.com/djangbahevans/goerp/sdk/go/crypto"
	"github.com/djangbahevans/goerp/sdk/go/engine"
	"github.com/vmihailenco/msgpack/v5"
)

const stripeTolerance = 5 * time.Minute

type request struct {
	Algo    string            `msgpack:"algo,omitempty"`
	Secrets [][]byte          `msgpack:"secrets,omitempty"`
	Headers map[string]string `msgpack:"headers,omitempty"`
	Body    []byte            `msgpack:"body,omitempty"`
	Sig     []byte            `msgpack:"sig,omitempty"`
	SigHex  string            `msgpack:"sig_hex,omitempty"`
	NowUnix int64             `msgpack:"now_unix,omitempty"`
	Hex     bool              `msgpack:"hex,omitempty"`
}

type result struct {
	Error   string `msgpack:"error,omitempty"`
	Valid   bool   `msgpack:"valid"`
	EventID string `msgpack:"event_id,omitempty"`
}

func verifyPrimitive(r request) bool {
	switch {
	case r.Algo == "sha256" && r.Hex:
		return crypto.VerifyHMACSHA256Hex(r.Secrets[0], r.Body, r.SigHex)
	case r.Algo == "sha256":
		return crypto.VerifyHMACSHA256(r.Secrets[0], r.Body, r.Sig)
	case r.Algo == "sha512" && r.Hex:
		return crypto.VerifyHMACSHA512Hex(r.Secrets[0], r.Body, r.SigHex)
	default:
		return crypto.VerifyHMACSHA512(r.Secrets[0], r.Body, r.Sig)
	}
}

// verifyPaystack accepts a body signed with any of secrets: Paystack sends the
// hex HMAC-SHA512 of the raw body in X-Paystack-Signature.
func verifyPaystack(secrets [][]byte, headers map[string]string, rawBody []byte) (bool, string, error) {
	sig := headers["X-Paystack-Signature"]
	matched := false
	for _, secret := range secrets {
		if crypto.VerifyHMACSHA512Hex(secret, rawBody, sig) {
			matched = true
			break
		}
	}
	if !matched {
		return false, "", nil
	}
	var event struct {
		Data struct {
			Reference string `json:"reference"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rawBody, &event); err != nil {
		return false, "", err
	}
	return true, event.Data.Reference, nil
}

// verifyStripe checks a Stripe-Signature header, a comma-separated list of one
// t= timestamp and one or more v1= HMAC-SHA256 hex signatures of "{t}.{body}".
// Any v1 signature made with any of secrets is accepted, and the timestamp
// must be within stripeTolerance of now.
func verifyStripe(secrets [][]byte, headers map[string]string, rawBody []byte, now time.Time) (bool, string, error) {
	var timestamp string
	var signatures []string
	for part := range strings.SplitSeq(headers["Stripe-Signature"], ",") {
		name, value, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			continue
		}
		switch name {
		case "t":
			timestamp = value
		case "v1":
			signatures = append(signatures, value)
		}
	}
	seconds, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil || len(signatures) == 0 {
		return false, "", nil
	}
	if now.Sub(time.Unix(seconds, 0)).Abs() > stripeTolerance {
		return false, "", nil
	}

	signedPayload := append([]byte(timestamp+"."), rawBody...)
	matched := false
	for _, secret := range secrets {
		for _, sig := range signatures {
			if crypto.VerifyHMACSHA256Hex(secret, signedPayload, sig) {
				matched = true
			}
		}
	}
	if !matched {
		return false, "", nil
	}
	var event struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rawBody, &event); err != nil {
		return false, "", err
	}
	return true, event.ID, nil
}

func readRequest(ptr, size uint32) (request, error) {
	var r request
	err := msgpack.Unmarshal(engine.ReadMem(ptr, size), &r)
	return r, err
}

func writeResult(r result) uint64 {
	data, err := msgpack.Marshal(r)
	if err != nil {
		data, _ = msgpack.Marshal(result{Error: "marshal result: " + err.Error()})
	}
	ptr := engine.Allocate(uint32(len(data)))
	engine.WriteMem(ptr, data)
	return uint64(ptr)<<32 | uint64(len(data))
}

func writeVerdict(valid bool, eventID string, err error) uint64 {
	if err != nil {
		return writeResult(result{Error: err.Error()})
	}
	return writeResult(result{Valid: valid, EventID: eventID})
}

//go:wasmexport run_verify_primitive
func runVerifyPrimitive(ptr, size uint32) uint64 {
	r, err := readRequest(ptr, size)
	if err != nil {
		return writeResult(result{Error: err.Error()})
	}
	return writeResult(result{Valid: verifyPrimitive(r)})
}

//go:wasmexport run_verify_paystack
func runVerifyPaystack(ptr, size uint32) uint64 {
	r, err := readRequest(ptr, size)
	if err != nil {
		return writeResult(result{Error: err.Error()})
	}
	return writeVerdict(verifyPaystack(r.Secrets, r.Headers, r.Body))
}

//go:wasmexport run_verify_stripe
func runVerifyStripe(ptr, size uint32) uint64 {
	r, err := readRequest(ptr, size)
	if err != nil {
		return writeResult(result{Error: err.Error()})
	}
	return writeVerdict(verifyStripe(r.Secrets, r.Headers, r.Body, time.Unix(r.NowUnix, 0)))
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
