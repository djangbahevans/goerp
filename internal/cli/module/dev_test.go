package module

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestDevRejectsOtherEnvironments(t *testing.T) {
	cmd := newDevCmd()
	cmd.SetContext(t.Context())
	cmd.Flags().String("env", "production", "Environment")
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs(nil)

	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "--env dev") {
		t.Fatalf("non-dev environment accepted: %v", err)
	}
}

func TestDevPorts(t *testing.T) {
	for _, opts := range []devOptions{
		{port: 0, adminPort: 8081, uiPort: 5173},
		{port: 8080, adminPort: 65536, uiPort: 5173},
		{port: 8080, adminPort: 8080, uiPort: 5173},
	} {
		if err := validateDevPorts(opts); err == nil {
			t.Fatalf("accepted invalid ports: %+v", opts)
		}
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	port := listener.Addr().(*net.TCPAddr).Port
	err = checkDevPort(t.Context(), port)
	if err == nil || !strings.Contains(err.Error(), strconv.Itoa(port)) || !strings.Contains(err.Error(), "PID "+strconv.Itoa(os.Getpid())) {
		t.Fatalf("collision must name the port and owning PID: %v", err)
	}
}

func TestDevRepo(t *testing.T) {
	repo := t.TempDir()
	for _, path := range []string{"compose.dev.yml", "cmd/engine/main.go", "shell/package.json"} {
		full := filepath.Join(repo, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	got, err := devRepo(filepath.Join(repo, "modules", "demo"), "")
	if err != nil || got != repo {
		t.Fatalf("locate checkout: got %q, %v", got, err)
	}
	if _, err := devRepo(repo, t.TempDir()); err == nil {
		t.Fatal("accepted an explicit directory without the engine and shell")
	}
}

func TestDevEngineEnv(t *testing.T) {
	t.Setenv("GOERP_DB_PRIMARY_DSN", "postgres://production")
	t.Setenv("GOERP_ADMIN_TOKEN", "production-token")
	t.Setenv("GOERP_STORAGE_BACKEND", "s3")
	values := make(map[string]string)
	for _, entry := range devEngineEnv("/repo", "/session/modules", "session-token", devOptions{port: 9090, adminPort: 9091, uiPort: 5174}) {
		key, value, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(key, "GOERP_") {
			if _, exists := values[key]; exists {
				t.Fatalf("duplicate engine environment key: %s", key)
			}
			values[key] = value
		}
	}

	for key, want := range map[string]string{
		"GOERP_ENV":             "development",
		"GOERP_MODULE_DEV":      "true",
		"GOERP_ADMIN_TOKEN":     "session-token",
		"GOERP_LISTEN_ADDR":     "127.0.0.1:9090",
		"GOERP_ADMIN_ADDR":      "127.0.0.1:9091",
		"GOERP_PLATFORM_DOMAIN": "localhost",
		"GOERP_STORAGE_BACKEND": "local",
		"GOERP_MODULE_DIR":      "/session/modules",
		"GOERP_APP_BASE_URL":    "http://localhost:5174",
	} {
		if values[key] != want {
			t.Errorf("%s = %q, want %q", key, values[key], want)
		}
	}
	if !strings.Contains(values["GOERP_DB_PRIMARY_DSN"], "/goerp_dev") {
		t.Fatal("engine does not use the local dev database")
	}
}

func TestDevContainerOwnership(t *testing.T) {
	containers, err := devContainers([]byte(`{"Service":"redis","State":"running","Publishers":[{"PublishedPort":6379}]}
{"Service":"postgres","State":"exited","Publishers":[{"PublishedPort":15432}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if !devPortOwned(containers, "redis", 6379) || devPortOwned(containers, "postgres", 15432) || devPortOwned(containers, "mailpit", 6379) {
		t.Fatal("only a running matching compose service may own its published port")
	}
}

func TestDevWaitHTTP(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Header.Get("Accept") != "text/html" {
			t.Error("shell probe must bypass the API proxy")
		}
		if requests == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = io.WriteString(w, "ready")
	}))
	defer server.Close()

	if err := waitDevHTTP(t.Context(), server.URL, true); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := waitDevHTTP(ctx, server.URL, false); err == nil {
		t.Fatal("cancelled startup continued polling")
	}
}

func newDevTestProcesses(t *testing.T) *devProcesses {
	t.Helper()
	ctx, cancel := context.WithCancelCause(t.Context())
	s := &devProcesses{ctx: ctx, cancel: cancel, stdout: io.Discard, stderr: io.Discard}
	t.Cleanup(s.stop)
	return s
}

func waitDevTestFile(t *testing.T, path string) []byte {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		data, err := os.ReadFile(path)
		if err == nil {
			return data
		}
		if time.Now().After(deadline) {
			t.Fatal(fmt.Errorf("wait for child marker: %w", err))
		}
		time.Sleep(10 * time.Millisecond)
	}
}
