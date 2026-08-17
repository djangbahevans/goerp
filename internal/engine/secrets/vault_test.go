package secrets

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// fakeVault is a minimal stand-in for Vault's HTTP API: kubernetes/approle
// login and KV v2 read/write, enough to exercise vaultBackend without a
// real Vault server.
type fakeVault struct {
	mu   sync.Mutex
	data map[string]string // full request path -> stored "value" field

	kubernetesLogins int
	approleLogins    int
}

func newFakeVaultServer(t *testing.T) (*httptest.Server, *fakeVault) {
	t.Helper()

	fv := &fakeVault{data: map[string]string{}}
	mux := http.NewServeMux()

	mux.HandleFunc("PUT /v1/auth/kubernetes/login", func(w http.ResponseWriter, r *http.Request) {
		fv.mu.Lock()
		fv.kubernetesLogins++
		fv.mu.Unlock()
		writeLoginResponse(w, "kube-token")
	})
	mux.HandleFunc("PUT /v1/auth/approle/login", func(w http.ResponseWriter, r *http.Request) {
		fv.mu.Lock()
		fv.approleLogins++
		fv.mu.Unlock()
		writeLoginResponse(w, "approle-token")
	})
	mux.HandleFunc("GET /v1/{mount}/data/{path...}", func(w http.ResponseWriter, r *http.Request) {
		fv.mu.Lock()
		value, ok := fv.data[r.URL.Path]
		fv.mu.Unlock()
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{"data": map[string]any{"value": value}},
		})
	})
	mux.HandleFunc("PUT /v1/{mount}/data/{path...}", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Data struct {
				Value string `json:"value"`
			} `json:"data"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		fv.mu.Lock()
		fv.data[r.URL.Path] = body.Data.Value
		fv.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"version": 1}})
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, fv
}

func writeLoginResponse(w http.ResponseWriter, token string) {
	_ = json.NewEncoder(w).Encode(map[string]any{
		"auth": map[string]any{
			"client_token":   token,
			"lease_duration": 3600,
			"renewable":      true,
		},
	})
}

func newFixtureJWT(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(path, []byte("fixture-jwt"), 0o600); err != nil {
		t.Fatalf("write fixture jwt: %v", err)
	}
	orig := kubernetesServiceAccountTokenPath
	kubernetesServiceAccountTokenPath = path
	t.Cleanup(func() { kubernetesServiceAccountTokenPath = orig })
	return path
}

func TestNewVaultBackend_MissingConfigFails(t *testing.T) {
	if _, err := newVaultBackend(); err == nil {
		t.Fatal("newVaultBackend() error = nil, want an error for missing required config")
	}
}

func TestNewVaultBackend_TokenAuth(t *testing.T) {
	srv, _ := newFakeVaultServer(t)
	t.Setenv("GOERP_VAULT_ADDR", srv.URL)
	t.Setenv("GOERP_VAULT_AUTH_METHOD", "token")
	t.Setenv("GOERP_VAULT_TOKEN", "static-token")

	backend, err := newVaultBackend()
	if err != nil {
		t.Fatalf("newVaultBackend() error: %v", err)
	}

	if err := backend.Set(context.Background(), "MY_KEY", "hunter2"); err != nil {
		t.Fatalf("Set() error: %v", err)
	}
	got, err := backend.Get(context.Background(), "MY_KEY")
	if err != nil {
		t.Fatalf("Get() error: %v", err)
	}
	if got != "hunter2" {
		t.Errorf("Get() = %q, want %q", got, "hunter2")
	}
}

func TestNewVaultBackend_KubernetesAuth(t *testing.T) {
	newFixtureJWT(t)
	srv, fv := newFakeVaultServer(t)
	t.Setenv("GOERP_VAULT_ADDR", srv.URL)
	t.Setenv("GOERP_VAULT_AUTH_METHOD", "kubernetes")
	t.Setenv("GOERP_VAULT_K8S_ROLE", "goerp-engine")

	if _, err := newVaultBackend(); err != nil {
		t.Fatalf("newVaultBackend() error: %v", err)
	}

	fv.mu.Lock()
	defer fv.mu.Unlock()
	if fv.kubernetesLogins != 1 {
		t.Errorf("kubernetesLogins = %d, want 1", fv.kubernetesLogins)
	}
}

func TestNewVaultBackend_ApproleAuth(t *testing.T) {
	srv, fv := newFakeVaultServer(t)
	t.Setenv("GOERP_VAULT_ADDR", srv.URL)
	t.Setenv("GOERP_VAULT_AUTH_METHOD", "approle")
	t.Setenv("GOERP_VAULT_APPROLE_ROLE_ID", "role-id")
	t.Setenv("GOERP_VAULT_APPROLE_SECRET_ID", "secret-id")

	if _, err := newVaultBackend(); err != nil {
		t.Fatalf("newVaultBackend() error: %v", err)
	}

	fv.mu.Lock()
	defer fv.mu.Unlock()
	if fv.approleLogins != 1 {
		t.Errorf("approleLogins = %d, want 1", fv.approleLogins)
	}
}

func TestVaultBackend_GetUnsetKeyReturnsEmptyNotError(t *testing.T) {
	srv, _ := newFakeVaultServer(t)
	t.Setenv("GOERP_VAULT_ADDR", srv.URL)
	t.Setenv("GOERP_VAULT_AUTH_METHOD", "token")
	t.Setenv("GOERP_VAULT_TOKEN", "static-token")

	backend, err := newVaultBackend()
	if err != nil {
		t.Fatalf("newVaultBackend() error: %v", err)
	}

	got, err := backend.Get(context.Background(), "NEVER_SET")
	if err != nil {
		t.Fatalf("Get() error: %v", err)
	}
	if got != "" {
		t.Errorf("Get() = %q, want empty string for an unset key", got)
	}
}

func TestVaultBackend_SecretPathFollowsNamingConvention(t *testing.T) {
	srv, fv := newFakeVaultServer(t)
	t.Setenv("GOERP_VAULT_ADDR", srv.URL)
	t.Setenv("GOERP_VAULT_AUTH_METHOD", "token")
	t.Setenv("GOERP_VAULT_TOKEN", "static-token")
	t.Setenv("GOERP_VAULT_KV_MOUNT", "custom-mount")
	t.Setenv("GOERP_ENV", "staging")

	backend, err := newVaultBackend()
	if err != nil {
		t.Fatalf("newVaultBackend() error: %v", err)
	}
	if err := backend.Set(context.Background(), "GOERP_ADMIN_TOKEN", "v"); err != nil {
		t.Fatalf("Set() error: %v", err)
	}

	fv.mu.Lock()
	defer fv.mu.Unlock()
	wantPath := "/v1/custom-mount/data/goerp/staging/GOERP_ADMIN_TOKEN"
	if _, ok := fv.data[wantPath]; !ok {
		t.Errorf("expected a write at %q, got paths: %v", wantPath, fv.data)
	}
}

func TestVaultBackend_Rotate(t *testing.T) {
	srv, _ := newFakeVaultServer(t)
	t.Setenv("GOERP_VAULT_ADDR", srv.URL)
	t.Setenv("GOERP_VAULT_AUTH_METHOD", "token")
	t.Setenv("GOERP_VAULT_TOKEN", "static-token")

	backend, err := newVaultBackend()
	if err != nil {
		t.Fatalf("newVaultBackend() error: %v", err)
	}

	rotated, err := backend.Rotate(context.Background(), "MY_KEY")
	if err != nil {
		t.Fatalf("Rotate() error: %v", err)
	}
	if rotated == "" {
		t.Fatal("Rotate() returned an empty value")
	}

	got, err := backend.Get(context.Background(), "MY_KEY")
	if err != nil {
		t.Fatalf("Get() error: %v", err)
	}
	if got != rotated {
		t.Errorf("Get() after Rotate() = %q, want %q", got, rotated)
	}

	again, err := backend.Rotate(context.Background(), "MY_KEY")
	if err != nil {
		t.Fatalf("second Rotate() error: %v", err)
	}
	if again == rotated {
		t.Error("second Rotate() returned the same value as the first — not actually random")
	}
}

func TestNew_VaultDispatchesToVaultBackend(t *testing.T) {
	srv, _ := newFakeVaultServer(t)
	t.Setenv("GOERP_VAULT_ADDR", srv.URL)
	t.Setenv("GOERP_VAULT_AUTH_METHOD", "token")
	t.Setenv("GOERP_VAULT_TOKEN", "static-token")

	backend, err := New("vault")
	if err != nil {
		t.Fatalf("New(\"vault\") error: %v", err)
	}
	if backend == nil {
		t.Fatal("New(\"vault\") returned a nil backend")
	}
}

func TestAuthenticator_UnsupportedMethodFails(t *testing.T) {
	if _, err := authenticator(nil, VaultConfig{VaultAuthMethod: "nonsense"}); err == nil {
		t.Fatal("authenticator() error = nil, want an error for an unsupported auth method")
	}
}
