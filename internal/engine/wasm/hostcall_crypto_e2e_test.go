package wasm

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"hash"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/config"
	"github.com/djangbahevans/goerp/internal/engine/wasm/wasmtest"
	"github.com/vmihailenco/msgpack/v5"
)

func compileHMACVerifierFixture(t *testing.T) []byte {
	t.Helper()

	wasmPath := filepath.Join(t.TempDir(), "hmacverifierfixture.wasm")
	cmd := exec.Command("go", "build", "-buildmode=c-shared", "-o", wasmPath, "./testdata/hmacverifierfixture")
	cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("compile testdata/hmacverifierfixture: %v\n%s", err, out)
	}

	data, err := os.ReadFile(wasmPath)
	if err != nil {
		t.Fatalf("read compiled fixture: %v", err)
	}
	return data
}

// hmacVerifierRequest and hmacVerifierResult mirror
// testdata/hmacverifierfixture's request and result envelopes.
type hmacVerifierRequest struct {
	Algo    string            `msgpack:"algo,omitempty"`
	Secrets [][]byte          `msgpack:"secrets,omitempty"`
	Headers map[string]string `msgpack:"headers,omitempty"`
	Body    []byte            `msgpack:"body,omitempty"`
	Sig     []byte            `msgpack:"sig,omitempty"`
	SigHex  string            `msgpack:"sig_hex,omitempty"`
	NowUnix int64             `msgpack:"now_unix,omitempty"`
	Hex     bool              `msgpack:"hex,omitempty"`
}

type hmacVerifierResult struct {
	Error   string `msgpack:"error,omitempty"`
	Valid   bool   `msgpack:"valid"`
	EventID string `msgpack:"event_id,omitempty"`
}

func hmacSum(newHash func() hash.Hash, key, data []byte) []byte {
	mac := hmac.New(newHash, key)
	mac.Write(data)
	return mac.Sum(nil)
}

// newHMACVerifierCaller returns a function that invokes one of the fixture's
// verifier exports with a request written into the module's own memory.
func newHMACVerifierCaller(t *testing.T) func(export string, req hmacVerifierRequest) hmacVerifierResult {
	t.Helper()

	ctx := t.Context()
	rt, err := New(&config.Config{
		CompilationCache:  wasmtest.SharedCompilationCacheDir(),
		Environment:       string(config.Production),
		PoolMaxMemoryByes: 8 << 20,
	}, nil, nil, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = rt.Close(context.Background()) })

	compiled, err := rt.wazero.CompileModule(ctx, compileHMACVerifierFixture(t))
	if err != nil {
		t.Fatalf("CompileModule: %v", err)
	}
	t.Cleanup(func() { _ = compiled.Close(context.Background()) })

	inst, err := newModuleInstance(ctx, fmt.Sprintf("hmacverifierfixture-%d", time.Now().UnixNano()), compiled, rt)
	if err != nil {
		t.Fatalf("newModuleInstance: %v", err)
	}
	inst.SetModuleContext(NewModuleContext("req-1", "testmodule", "user-1", "", nil, nil, "tenant-1", "tenant-slug", "trace-1", 0, nil, ModuleSnapshot{}))
	rt.RegisterInstance(inst)
	t.Cleanup(func() { rt.UnregisterInstance(inst) })

	return func(export string, req hmacVerifierRequest) hmacVerifierResult {
		t.Helper()

		payload, err := msgpack.Marshal(req)
		if err != nil {
			t.Fatalf("marshal request: %v", err)
		}
		alloc, err := inst.module.ExportedFunction("allocate").Call(ctx, uint64(len(payload)))
		if err != nil {
			t.Fatalf("allocate: %v", err)
		}
		ptr := uint32(alloc[0])
		if !inst.module.Memory().Write(ptr, payload) {
			t.Fatal("write request: out of bounds")
		}

		results, err := inst.module.ExportedFunction(export).Call(ctx, uint64(ptr), uint64(len(payload)))
		if err != nil {
			t.Fatalf("call %s: %v", export, err)
		}
		raw, ok := inst.module.Memory().Read(uint32(results[0]>>32), uint32(results[0]))
		if !ok {
			t.Fatalf("read %s result: out of bounds", export)
		}
		var out hmacVerifierResult
		if err := msgpack.Unmarshal(raw, &out); err != nil {
			t.Fatalf("unmarshal %s result: %v", export, err)
		}
		if out.Error != "" {
			t.Fatalf("%s failed: %s", export, out.Error)
		}
		return out
	}
}

// TestHMACVerifierFixture_Primitives_MatchRFC4231 drives the SDK helpers
// through the real host.crypto.verify_hmac with the HMAC test vectors of
// RFC 4231 §4.2 and §4.3, and checks that each rejects a corrupted input.
func TestHMACVerifierFixture_Primitives_MatchRFC4231(t *testing.T) {
	call := newHMACVerifierCaller(t)

	vectors := []struct {
		name   string
		key    []byte
		data   []byte
		sha256 string
		sha512 string
	}{
		{
			name:   "RFC 4231 test case 1",
			key:    []byte("\x0b\x0b\x0b\x0b\x0b\x0b\x0b\x0b\x0b\x0b\x0b\x0b\x0b\x0b\x0b\x0b\x0b\x0b\x0b\x0b"),
			data:   []byte("Hi There"),
			sha256: "b0344c61d8db38535ca8afceaf0bf12b881dc200c9833da726e9376c2e32cff7",
			sha512: "87aa7cdea5ef619d4ff0b4241a1d6cb02379f4e2ce4ec2787ad0b30545e17cdedaa833b7d6b8a702038b274eaea3f4e4be9d914eeb61f1702e696c203a126854",
		},
		{
			name:   "RFC 4231 test case 2",
			key:    []byte("Jefe"),
			data:   []byte("what do ya want for nothing?"),
			sha256: "5bdcc146bf60754e6a042426089575c75a003f089d2739839dec58b964ec3843",
			sha512: "164b7a7bfcf819e2e395fbe73b56e0a387bd64222e831fd610270cd7ea2505549758bf75c05a994a6d034f65f8f0e6fdcaeab1a34d4a6b4b636e070a38bce737",
		},
	}

	for _, v := range vectors {
		for _, algo := range []struct{ name, sigHex string }{{"sha256", v.sha256}, {"sha512", v.sha512}} {
			sig, err := hex.DecodeString(algo.sigHex)
			if err != nil {
				t.Fatalf("decode vector: %v", err)
			}

			t.Run(v.name+"/"+algo.name, func(t *testing.T) {
				req := func(mutate func(*hmacVerifierRequest)) bool {
					r := hmacVerifierRequest{Algo: algo.name, Secrets: [][]byte{v.key}, Body: v.data, Sig: sig, SigHex: algo.sigHex}
					mutate(&r)
					return call("run_verify_primitive", r).Valid
				}

				if !req(func(r *hmacVerifierRequest) {}) {
					t.Error("raw signature rejected, want accepted")
				}
				if !req(func(r *hmacVerifierRequest) { r.Hex = true }) {
					t.Error("hex signature rejected, want accepted")
				}
				if req(func(r *hmacVerifierRequest) { r.Secrets = [][]byte{append([]byte("x"), v.key...)} }) {
					t.Error("wrong key accepted")
				}
				if req(func(r *hmacVerifierRequest) { r.Body = append([]byte("x"), v.data...) }) {
					t.Error("tampered data accepted")
				}
				if req(func(r *hmacVerifierRequest) { r.Sig = sig[:len(sig)-1] }) {
					t.Error("truncated signature accepted")
				}
				if req(func(r *hmacVerifierRequest) { r.Hex = true; r.SigHex = algo.sigHex[:len(algo.sigHex)-1] }) {
					t.Error("odd-length hex signature accepted")
				}
				if req(func(r *hmacVerifierRequest) { r.Hex = true; r.SigHex = "zz" + algo.sigHex[2:] }) {
					t.Error("non-hex signature accepted")
				}
				if req(func(r *hmacVerifierRequest) { r.Hex = true; r.SigHex = "" }) {
					t.Error("empty hex signature accepted")
				}
			})
		}
	}
}

func TestHMACVerifierFixture_CrossAlgorithmSignatureRejected(t *testing.T) {
	call := newHMACVerifierCaller(t)
	key, body := []byte("secret"), []byte("payload")

	got := call("run_verify_primitive", hmacVerifierRequest{
		Algo: "sha512", Secrets: [][]byte{key}, Body: body, Sig: hmacSum(sha256.New, key, body),
	})
	if got.Valid {
		t.Error("a SHA-256 signature verified as SHA-512")
	}
}

// TestHMACVerifierFixture_PaystackVerifier checks the example verifier
// against Paystack's documented scheme: the hex HMAC-SHA512 of the raw body
// in X-Paystack-Signature.
func TestHMACVerifierFixture_PaystackVerifier(t *testing.T) {
	call := newHMACVerifierCaller(t)

	body := []byte(`{"event":"charge.success","data":{"reference":"ref_8f2a"}}`)
	current, previous := []byte("sk_live_current"), []byte("sk_live_previous")
	signWith := func(newHash func() hash.Hash, key []byte) map[string]string {
		return map[string]string{"X-Paystack-Signature": hex.EncodeToString(hmacSum(newHash, key, body))}
	}

	tests := []struct {
		name    string
		secrets [][]byte
		headers map[string]string
		want    bool
	}{
		{"signed with the current secret", [][]byte{current, previous}, signWith(sha512.New, current), true},
		{"signed with the previous secret during rotation", [][]byte{current, previous}, signWith(sha512.New, previous), true},
		{"signed with an unknown secret", [][]byte{current}, signWith(sha512.New, []byte("other")), false},
		{"SHA-256 signature", [][]byte{current}, signWith(sha256.New, current), false},
		{"missing header", [][]byte{current}, nil, false},
		{"no secrets configured", nil, signWith(sha512.New, current), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := call("run_verify_paystack", hmacVerifierRequest{Secrets: tt.secrets, Headers: tt.headers, Body: body})
			if got.Valid != tt.want {
				t.Fatalf("valid = %v, want %v", got.Valid, tt.want)
			}
			if tt.want && got.EventID != "ref_8f2a" {
				t.Errorf("event ID = %q, want ref_8f2a", got.EventID)
			}
			if !tt.want && got.EventID != "" {
				t.Errorf("event ID = %q for an invalid delivery, want none", got.EventID)
			}
		})
	}
}

// TestHMACVerifierFixture_StripeVerifier checks the example verifier against
// Stripe's documented scheme: a Stripe-Signature header of one t= timestamp
// and one or more v1= hex HMAC-SHA256 signatures over "{t}.{body}", valid
// within a 5-minute tolerance.
func TestHMACVerifierFixture_StripeVerifier(t *testing.T) {
	call := newHMACVerifierCaller(t)

	body := []byte(`{"id":"evt_1Nx","type":"invoice.paid"}`)
	current, previous := []byte("whsec_current"), []byte("whsec_previous")
	now := time.Unix(1_750_000_000, 0)

	v1 := func(key []byte, ts int64) string {
		signed := strconv.FormatInt(ts, 10) + "." + string(body)
		return "v1=" + hex.EncodeToString(hmacSum(sha256.New, key, []byte(signed)))
	}
	header := func(ts int64, parts ...string) map[string]string {
		h := "t=" + strconv.FormatInt(ts, 10)
		for _, p := range parts {
			h += "," + p
		}
		return map[string]string{"Stripe-Signature": h}
	}
	ts := now.Unix()

	tests := []struct {
		name    string
		secrets [][]byte
		headers map[string]string
		want    bool
	}{
		{"single valid signature", [][]byte{current}, header(ts, v1(current, ts)), true},
		{"second v1 matches while the first is bogus", [][]byte{current}, header(ts, "v1=00ff", v1(current, ts)), true},
		{"signed with the previous secret during rotation", [][]byte{current, previous}, header(ts, v1(previous, ts)), true},
		{"unknown secret", [][]byte{current}, header(ts, v1([]byte("other"), ts)), false},
		{"timestamp 4m59s old", [][]byte{current}, header(ts-299, v1(current, ts-299)), true},
		{"timestamp 6m old", [][]byte{current}, header(ts-360, v1(current, ts-360)), false},
		{"timestamp 6m in the future", [][]byte{current}, header(ts+360, v1(current, ts+360)), false},
		{"valid signature for a different timestamp", [][]byte{current}, header(ts, v1(current, ts-1)), false},
		{"no v1 signature", [][]byte{current}, header(ts), false},
		{"no timestamp", [][]byte{current}, map[string]string{"Stripe-Signature": v1(current, ts)}, false},
		{"non-numeric timestamp", [][]byte{current}, map[string]string{"Stripe-Signature": "t=abc," + v1(current, ts)}, false},
		{"missing header", [][]byte{current}, nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := call("run_verify_stripe", hmacVerifierRequest{Secrets: tt.secrets, Headers: tt.headers, Body: body, NowUnix: now.Unix()})
			if got.Valid != tt.want {
				t.Fatalf("valid = %v, want %v", got.Valid, tt.want)
			}
			if tt.want && got.EventID != "evt_1Nx" {
				t.Errorf("event ID = %q, want evt_1Nx", got.EventID)
			}
		})
	}
}
