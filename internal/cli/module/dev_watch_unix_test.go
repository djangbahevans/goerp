//go:build linux || darwin

package module

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/cli/adminclient"
)

func TestDevSelectedSetBuildAndWatch(t *testing.T) {
	repo := t.TempDir()
	parent := filepath.Join(repo, "modules")
	current := writeDevSource(t, filepath.Join(parent, "sales-source"), "sales", "contacts")
	dependency := writeDevSource(t, filepath.Join(parent, "contacts-source"), "contacts")
	writeDevManifest(t, dependency, map[string]any{"name": "contacts", "frontend": map[string]any{"bundle": true}})
	writeDevSource(t, filepath.Join(parent, "unrelated"), "unrelated")
	sources, err := resolveDevSources(current, repo, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range sources {
		if err := os.WriteFile(filepath.Join(source.dir, "state.json"), []byte("initial"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	log := filepath.Join(repo, "builds")
	t.Setenv("DEV_BUILD_LOG", log)
	cliPath := filepath.Join(repo, "goerp")
	script := `#!/bin/sh
source=$3
printf '%s\n' "$*" >> "$DEV_BUILD_LOG"
shift 3
while [ "$#" -gt 0 ]; do
  case "$1" in
    --output) output=$2; shift ;;
  esac
  shift
done
value=$(cat "$source/state.json")
if [ "$value" = compile_failure ]; then exit 1; fi
printf '%s' "$value" > "$output"
printf '%s' "$value" > "$output.sha256"
`
	if err := os.WriteFile(cliPath, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	session := t.TempDir()
	modules := filepath.Join(session, "modules")
	if err := os.Mkdir(modules, 0o700); err != nil {
		t.Fatal(err)
	}
	processes := newDevTestProcesses(t)
	if err := processes.buildDevSources(repo, cliPath, modules, "/worker-storage", sources); err != nil {
		t.Fatal(err)
	}
	commands, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(commands)), "\n")
	if len(lines) != 2 || !strings.Contains(lines[0], dependency) || strings.Contains(lines[0], "--no-frontend") || !strings.Contains(lines[1], current) || !strings.Contains(lines[1], "--no-frontend") {
		t.Fatalf("selected dependency-first builds must package declared frontends once: %s", commands)
	}
	for _, line := range lines {
		if !strings.Contains(line, "--worker-storage /worker-storage") {
			t.Fatalf("worker storage missing: %s", line)
		}
	}

	reloads := make(chan string, 10)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		reloads <- r.URL.Path + ":" + string(data)
		if string(data) == "rejected" {
			http.Error(w, "rejected schema", http.StatusConflict)
			return
		}
		_, _ = io.WriteString(w, `{}`)
	}))
	defer server.Close()
	client, err := adminclient.New(server.URL, "test-token", time.Second)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancelCause(t.Context())
	defer cancel(context.Canceled)
	var mu sync.Mutex
	var diagnostics bytes.Buffer
	stderr := devWriter{mu: &mu, writer: &diagnostics}
	stop := watchDevSources(ctx, cancel, sources, repo, cliPath, session, modules, "/worker-storage", client, io.Discard, stderr)
	defer stop()
	unchanged, err := os.ReadFile(log)
	if err != nil || !bytes.Equal(unchanged, commands) {
		t.Fatalf("watch startup rebuilt existing packages: %s, %v", unchanged, err)
	}

	waitDiagnostic := func(fragment string) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for {
			mu.Lock()
			message := diagnostics.String()
			mu.Unlock()
			if strings.Contains(message, fragment) {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("missing diagnostic %q: %s", fragment, message)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	setState := func(dir, value string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, "state.json"), []byte(value), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	expectReload := func(name, value string) {
		t.Helper()
		select {
		case got := <-reloads:
			if want := "/admin/dev/modules/" + name + "/reload:" + value; got != want {
				t.Fatalf("reload = %q, want %q", got, want)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("selected module change did not reload")
		}
	}
	expectPackage := func(name, want string) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for {
			data, err := os.ReadFile(filepath.Join(modules, name+".erp"))
			if err == nil && string(data) == want {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("package %s = %q, %v, want %q", name, data, err, want)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}

	setState(dependency, "compile_failure")
	waitDiagnostic("build contacts:")
	expectPackage("contacts", "initial")
	setState(dependency, "accepted")
	expectReload("contacts", "accepted")
	expectPackage("contacts", "accepted")
	setState(dependency, "rejected")
	expectReload("contacts", "rejected")
	waitDiagnostic("reload contacts:")
	expectPackage("contacts", "accepted")
	setState(dependency, "recovered")
	expectReload("contacts", "recovered")
	expectPackage("contacts", "recovered")
	setState(current, "current-edited")
	expectReload("sales", "current-edited")
	expectPackage("sales", "current-edited")

	writeDevManifest(t, dependency, map[string]any{"name": "contacts", "depends_on": []string{"new_dependency"}})
	waitDiagnostic("restart goerp module dev")
	expectPackage("contacts", "recovered")
	if ctx.Err() != nil {
		t.Fatalf("recoverable build failures ended the session: %v", context.Cause(ctx))
	}

	cancel(context.Canceled)
	stop()
	if entries, err := os.ReadDir(session); err != nil || len(entries) != 1 {
		t.Fatalf("watch shutdown leaves candidate directories: %v, %v", entries, err)
	}
}
