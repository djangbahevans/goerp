package wasm

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/djangbahevans/goerp/internal/engine/config"
	"github.com/vmihailenco/msgpack/v5"
)

func TestHTTPCallerFixture_SDKThroughRealRuntime(t *testing.T) {
	db := openTestPrimaryDB(t)
	fetcher, _ := newHTTPTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirect" {
			w.Header().Set("Location", "/final")
			w.WriteHeader(307)
			return
		}

		body, _ := io.ReadAll(r.Body)
		w.Header().Set("X-Provider", "fixture")
		w.WriteHeader(201)
		_, _ = fmt.Fprintf(w, "%s:%s", r.Method, body)
	})

	r, err := New(&config.Config{
		CompilationCache:  sharedTestCompilationCacheDir(),
		Environment:       string(config.Production),
		PoolMaxMemoryByes: 32 << 20,
	}, db, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = r.Close(context.Background()) })
	r.httpFetcher = fetcher
	wasmPath := filepath.Join(t.TempDir(), "httpcallerfixture.wasm")
	cmd := exec.CommandContext(t.Context(), "go", "build", "-buildmode=c-shared", "-o", wasmPath, "./testdata/httpcallerfixture")
	cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("compile HTTP fixture: %v\n%s", err, out)
	}

	wasmBytes, err := os.ReadFile(wasmPath)
	if err != nil {
		t.Fatal(err)
	}

	compiled, err := r.wazero.CompileModule(t.Context(), wasmBytes)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = compiled.Close(context.Background()) })
	inst, err := newModuleInstance(t.Context(), "httpcallerfixture", compiled, r.wazero)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = inst.module.Close(context.Background()) })
	r.RegisterInstance(inst)
	t.Cleanup(func() { r.UnregisterInstance(inst) })

	for _, tt := range []struct {
		name   string
		caps   abi.CapabilitySet
		input  abiv1.HTTPFetchInput
		status int
		body   string
		code   string
	}{
		{"default redirects", abi.CapHTTPFetch, abiv1.HTTPFetchInput{URL: "https://api.example.com/redirect", Method: "POST", Body: []byte("sdk")}, 201, "POST:sdk", ""},
		{"disabled redirects", abi.CapHTTPFetch, abiv1.HTTPFetchInput{URL: "https://api.example.com/redirect", FollowRedirects: new(false)}, 307, "", ""},
		{"default method", abi.CapHTTPFetch, abiv1.HTTPFetchInput{URL: "https://api.example.com/final"}, 201, "GET:", ""},
		{"capability denied", 0, abiv1.HTTPFetchInput{URL: "https://api.example.com/final"}, 0, "", abiv1.ErrCodeCapabilityDenied},
		{"domain denied", abi.CapHTTPFetch, abiv1.HTTPFetchInput{URL: "https://attacker.test/"}, 0, "", abiv1.ErrCodeHTTPDomainNotAllowed},
		{"missing allowlist", abi.CapHTTPFetch, abiv1.HTTPFetchInput{URL: "https://api.example.com/final"}, 0, "", abiv1.ErrCodeHTTPDomainNotAllowed},
	} {
		t.Run(tt.name, func(t *testing.T) {
			mc := newHTTPTestContext(tt.caps)
			if tt.name == "missing allowlist" {
				mc.snapshot.HTTPAllowlist = nil
			}

			inst.SetModuleContext(mc)
			env := callHost(t, t.Context(), inst, "run_fetch", tt.input)
			if tt.code != "" {
				if env.OK {
					t.Fatal("expected host failure")
				}

				requireHTTPError(t, env.Error, tt.code)
				return
			}

			if !env.OK {
				t.Fatalf("SDK fetch failed: %v", env.Error)
			}

			var out abiv1.HTTPFetchOutput
			if err := msgpack.Unmarshal(env.Data, &out); err != nil {
				t.Fatal(err)
			}

			if out.StatusCode != tt.status || string(out.Body) != tt.body || out.DurationMs <= 0 {
				t.Fatalf("output = %+v", out)
			}

			if tt.status == 201 && out.Headers["X-Provider"] != "fixture" {
				t.Errorf("headers = %v", out.Headers)
			}
		})
	}

	t.Run("missing execution context", func(t *testing.T) {
		inst.SetModuleContext(nil)
		env := callHost(t, t.Context(), inst, "run_fetch", abiv1.HTTPFetchInput{URL: "https://api.example.com/final"})
		requireHTTPError(t, env.Error, abiv1.ErrCodeUnavailable)
	})

	t.Run("unregistered instance", func(t *testing.T) {
		inst.SetModuleContext(newHTTPTestContext(abi.CapHTTPFetch))
		r.UnregisterInstance(inst)
		env := callHost(t, t.Context(), inst, "run_fetch", abiv1.HTTPFetchInput{URL: "https://api.example.com/final"})
		requireHTTPError(t, env.Error, abiv1.ErrCodeUnavailable)
	})
}

func TestHTTPFetch_ModuleSnapshotIsIndependent(t *testing.T) {
	allowlist := []string{"https://example.com"}
	mc := NewModuleContext("", "connector", "", "", nil, nil, "", "", "", abi.CapHTTPFetch, nil, ModuleSnapshot{HTTPAllowlist: allowlist})
	allowlist[0] = "https://attacker.test"
	if err := validateHTTPAllowlist("attacker.test", mc.snapshot.HTTPAllowlist); err == nil {
		t.Fatal("mutating the manifest changed the in-flight request's allowlist")
	}
}
