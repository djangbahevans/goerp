package wasm

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/config"
	"github.com/djangbahevans/goerp/internal/engine/wasm/wasmtest"
	"github.com/tetratelabs/wazero"
)

func compileTestdata(t *testing.T, dir string) []byte {
	t.Helper()

	wasmPath := filepath.Join(t.TempDir(), dir+".wasm")
	cmd := exec.Command("go", "build", "-buildmode=c-shared", "-o", wasmPath, "./testdata/"+dir)
	cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("compile testdata/%s: %v\n%s", dir, err, out)
	}

	data, err := os.ReadFile(wasmPath)
	if err != nil {
		t.Fatalf("read compiled fixture: %v", err)
	}
	return data
}

// webhookVerifierEnv is a runtime with the fixture compiled, a one-instance
// pool over it, and the config resolver and store wired so a call that
// escaped the verifier restriction would succeed instead of failing for lack
// of a backing service.
type webhookVerifierEnv struct {
	rt       *Runtime
	compiled wazero.CompiledModule
	pool     *InstancePool
	// drainPool drains the pool once, whether a test or cleanup gets there first.
	drainPool func()
}

func newWebhookVerifierEnv(t *testing.T, fixture string) *webhookVerifierEnv {
	t.Helper()
	ctx := context.Background()

	rt, err := New(&config.Config{
		CompilationCache:  wasmtest.SharedCompilationCacheDir(),
		Environment:       string(config.Production),
		PoolMaxMemoryByes: 8 << 20,
	}, nil, nil, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = rt.Close(context.Background()) })

	store := &memoryConfig{values: map[string]string{}}
	rt.SetTenantConfig(store, store)

	compiled, err := rt.wazero.CompileModule(ctx, compileTestdata(t, fixture))
	if err != nil {
		t.Fatalf("CompileModule: %v", err)
	}
	t.Cleanup(func() { _ = compiled.Close(context.Background()) })

	pool := rt.NewPool("webhookmod", compiled, PoolConfig{MaxSize: 2, BorrowTimeout: time.Second})
	env := &webhookVerifierEnv{rt: rt, compiled: compiled, pool: pool}
	env.drainPool = sync.OnceFunc(func() { pool.DrainAndClose(context.Background(), time.Second) })
	t.Cleanup(env.drainPool)

	return env
}

func (e *webhookVerifierEnv) verify(t *testing.T, src WebhookVerifierSource, mode string, extra map[string]string, secrets [][]byte, body []byte) WebhookVerdict {
	t.Helper()
	headers := map[string][]string{"X-Mode": {mode}}
	for k, v := range extra {
		headers[k] = []string{v}
	}
	return e.rt.VerifyWebhook(t.Context(), src, abiv1.WebhookVerifyRequest{Secrets: secrets, Headers: headers, Body: body})
}

func (e *webhookVerifierEnv) pooled() WebhookVerifierSource {
	return WebhookVerifierSource{ModuleName: "webhookmod", Pool: e.pool, Compiled: e.compiled}
}

func TestVerifyWebhook_Outcomes(t *testing.T) {
	env := newWebhookVerifierEnv(t, "webhookverifierfixture")

	tests := []struct {
		name        string
		mode        string
		extra       map[string]string
		wantOutcome WebhookOutcome
		wantEventID string
		wantMessage string
	}{
		{"valid signature", "valid", map[string]string{"X-Event-Id": "evt_9"}, WebhookAccepted, "evt_9", ""},
		{"invalid signature", "invalid", nil, WebhookRejected, "", ""},
		{"verifier error", "error", nil, WebhookVerifierError, "", "malformed delivery"},
		{"verifier panic", "panic", nil, WebhookVerifierError, "", "verifier panicked: verifier bug"},
		{"valid without an event ID", "no-id", nil, WebhookVerifierError, "", "without a provider event ID"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := env.verify(t, env.pooled(), tt.mode, tt.extra, nil, nil)
			if got.Outcome != tt.wantOutcome || got.ProviderEventID != tt.wantEventID || !strings.Contains(got.Message, tt.wantMessage) {
				t.Errorf("verdict = %+v, want outcome %d event %q message containing %q", got, tt.wantOutcome, tt.wantEventID, tt.wantMessage)
			}
		})
	}
}

func TestVerifyWebhook_PassesSecretsHeadersAndBody(t *testing.T) {
	env := newWebhookVerifierEnv(t, "webhookverifierfixture")

	got := env.verify(t, env.pooled(), "echo", nil, [][]byte{[]byte("current"), []byte("previous")}, []byte(`{"id":1}`))
	if got.Outcome != WebhookAccepted || got.ProviderEventID != `2 secrets, body {"id":1}` {
		t.Errorf("verdict = %+v, want accepted echoing 2 secrets and the body", got)
	}
}

// The verifier can use host.crypto, and nothing else: its first secret fails,
// the second matches.
func TestVerifyWebhook_HostCryptoIsAvailable(t *testing.T) {
	env := newWebhookVerifierEnv(t, "webhookverifierfixture")
	body := []byte(`{"id":"evt_h"}`)
	mac := hmac.New(sha256.New, []byte("previous"))
	mac.Write(body)
	extra := map[string]string{"X-Event-Id": "evt_h", "X-Signature": hex.EncodeToString(mac.Sum(nil))}

	got := env.verify(t, env.pooled(), "hmac", extra, [][]byte{[]byte("current"), []byte("previous")}, body)
	if got.Outcome != WebhookAccepted || got.ProviderEventID != "evt_h" {
		t.Errorf("verdict = %+v, want accepted with evt_h", got)
	}

	got = env.verify(t, env.pooled(), "hmac", extra, [][]byte{[]byte("current")}, body)
	if got.Outcome != WebhookRejected {
		t.Errorf("verdict = %+v, want rejected for a signature no secret made", got)
	}
}

func TestVerifyWebhook_OtherHostFunctionsAreDenied(t *testing.T) {
	env := newWebhookVerifierEnv(t, "webhookverifierfixture")

	for _, mode := range []string{"config-set", "enqueue"} {
		t.Run(mode, func(t *testing.T) {
			got := env.verify(t, env.pooled(), mode, nil, nil, nil)
			if got.Outcome != WebhookVerifierError || !strings.Contains(got.Message, abiv1.ErrCodeCapabilityDenied) {
				t.Errorf("verdict = %+v, want a verifier error carrying %s", got, abiv1.ErrCodeCapabilityDenied)
			}
		})
	}
}

// Host functions work again for the same module outside a verifier call: the
// restriction belongs to the call, not the module.
func TestVerifyWebhook_RestrictionEndsWithTheCall(t *testing.T) {
	env := newWebhookVerifierEnv(t, "webhookverifierfixture")
	env.verify(t, env.pooled(), "valid", map[string]string{"X-Event-Id": "e"}, nil, nil)

	inst, err := env.pool.Borrow(t.Context())
	if err != nil {
		t.Fatalf("Borrow: %v", err)
	}
	t.Cleanup(func() { env.pool.Return(inst) })
	if inst.ModuleContext() != nil {
		t.Error("instance kept the verifier's module context after the call")
	}
}

func TestVerifyWebhook_Timeout(t *testing.T) {
	env := newWebhookVerifierEnv(t, "webhookverifierfixture")
	original := webhookVerifyTimeout
	webhookVerifyTimeout = 300 * time.Millisecond
	t.Cleanup(func() { webhookVerifyTimeout = original })

	start := time.Now()
	got := env.verify(t, env.pooled(), "spin", nil, nil, nil)
	if got.Outcome != WebhookVerifierError || !strings.Contains(got.Message, "exceeded") {
		t.Errorf("verdict = %+v, want a timeout verifier error", got)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("a spinning verifier held the call for %s", elapsed)
	}

	if got := env.verify(t, env.pooled(), "valid", map[string]string{"X-Event-Id": "e"}, nil, nil); got.Outcome != WebhookAccepted {
		t.Errorf("verdict after a timeout = %+v, want accepted", got)
	}
}

// A disabled module's pool is drained, so the verifier runs on a transient
// instance of the compiled module.
func TestVerifyWebhook_DisabledModuleUsesTransientInstance(t *testing.T) {
	env := newWebhookVerifierEnv(t, "webhookverifierfixture")
	env.drainPool()

	got := env.verify(t, env.pooled(), "valid", map[string]string{"X-Event-Id": "evt_d"}, nil, nil)
	if got.Outcome != WebhookAccepted || got.ProviderEventID != "evt_d" {
		t.Errorf("verdict = %+v, want accepted with evt_d", got)
	}

	got = env.verify(t, WebhookVerifierSource{ModuleName: "webhookmod", Compiled: env.compiled}, "valid", map[string]string{"X-Event-Id": "evt_n"}, nil, nil)
	if got.Outcome != WebhookAccepted || got.ProviderEventID != "evt_n" {
		t.Errorf("verdict without a pool = %+v, want accepted with evt_n", got)
	}
}

func TestVerifyWebhook_NoVerifierExport(t *testing.T) {
	env := newWebhookVerifierEnv(t, "configcallerfixture")

	if got := env.verify(t, env.pooled(), "valid", nil, nil, nil); got.Outcome != WebhookNoVerifier {
		t.Errorf("verdict = %+v, want WebhookNoVerifier", got)
	}
}

func TestVerifyWebhook_SourceWithoutPoolOrModule(t *testing.T) {
	env := newWebhookVerifierEnv(t, "webhookverifierfixture")

	got := env.verify(t, WebhookVerifierSource{ModuleName: "webhookmod"}, "valid", nil, nil, nil)
	if got.Outcome != WebhookVerifierError {
		t.Errorf("verdict = %+v, want a verifier error", got)
	}
}
