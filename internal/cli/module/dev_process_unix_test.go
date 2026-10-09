//go:build linux || darwin

package module

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestDevPreparationCancellationAndForcedShutdown(t *testing.T) {
	s := newDevTestProcesses(t)
	dir := t.TempDir()
	marker := filepath.Join(dir, "ready")
	result := make(chan error, 1)
	go func() {
		result <- s.run(dir, nil, io.Discard, "sh", "-c", `trap '' TERM; echo ready > "$1"; sleep 60`, "sh", marker)
	}()
	waitDevTestFile(t, marker)

	start := time.Now()
	s.cancel(context.Canceled)
	err := <-result
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled preparation: %v", err)
	}
	if time.Since(start) < 4*time.Second {
		t.Fatal("a child ignoring SIGTERM did not receive its shutdown grace period")
	}
	if len(s.children) != 0 {
		t.Fatal("cancelled preparation retained a child process")
	}
}

func TestDevInfraReuseAndTeardown(t *testing.T) {
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
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	port := listener.Addr().(*net.TCPAddr).Port
	t.Setenv("TEST_COMPOSE_CONFIG", fmt.Sprintf(`{"services":{"postgres":{"ports":[{"published":"%d"}]},"pgbouncer":{},"redis":{},"mailpit":{}}}`, port))
	t.Setenv("TEST_COMPOSE_PS", fmt.Sprintf(`{"Service":"postgres","State":"running","Publishers":[{"PublishedPort":%d}]}`, port))
	log := filepath.Join(repo, "commands")
	t.Setenv("TEST_COMPOSE_LOG", log)
	t.Setenv("PATH", repo+string(os.PathListSeparator)+os.Getenv("PATH"))
	script := `#!/bin/sh
printf '%s\n' "$*" >> "$TEST_COMPOSE_LOG"
case "$*" in
  *'config --format json') printf '%s\n' "$TEST_COMPOSE_CONFIG" ;;
  *'ps --format json') printf '%s\n' "$TEST_COMPOSE_PS" ;;
esac
`
	if err := os.WriteFile(filepath.Join(repo, "docker"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	s := newDevTestProcesses(t)
	if err := s.startInfra(repo); err != nil {
		t.Fatalf("reuse recognized occupied port: %v", err)
	}
	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "--project-name goerp") || !strings.Contains(string(data), "up -d --wait --wait-timeout 120 --no-recreate postgres pgbouncer redis mailpit") || strings.Contains(string(data), " down\n") {
		t.Fatalf("unexpected startup commands: %s", data)
	}

	t.Chdir(repo)
	if err := runDev(t.Context(), io.Discard, io.Discard, devOptions{teardown: true}); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if !strings.HasSuffix(lines[len(lines)-1], " down") || strings.Contains(lines[len(lines)-1], "-v") || len(lines) != 4 {
		t.Fatalf("teardown must only remove containers and preserve volumes: %s", data)
	}
	if _, err := os.Stat(filepath.Join(repo, ".dev")); !os.IsNotExist(err) {
		t.Fatal("teardown started a session")
	}
}

func TestDevShutdownReverseOrderAndDescendants(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "stopped")
	s := newDevTestProcesses(t)
	var children []*devProcess
	for _, name := range []string{"engine", "shell"} {
		marker := filepath.Join(dir, name)
		script := `trap 'printf "%s\n" "$1" >> "$2"; exit 0' TERM
sleep 60 &
echo ready > "$3"
wait`
		p, err := s.start(dir, nil, io.Discard, true, "sh", "-c", script, "sh", name, log, marker)
		if err != nil {
			t.Fatal(err)
		}
		children = append(children, p)
		waitDevTestFile(t, marker)
	}

	s.stop()
	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "shell\nengine\n" {
		t.Fatalf("shutdown order: %q", data)
	}
	for _, p := range children {
		if err := syscall.Kill(p.cmd.Process.Pid, 0); err == nil {
			t.Fatalf("child PID %d survived shutdown", p.cmd.Process.Pid)
		}
	}
}

func TestDevChildFailureCancelsSession(t *testing.T) {
	s := newDevTestProcesses(t)
	p, err := s.start(t.TempDir(), nil, io.Discard, true, "sh", "-c", "exit 7")
	if err != nil {
		t.Fatal(err)
	}
	<-p.done
	<-s.ctx.Done()
	if !strings.Contains(s.ctx.Err().Error(), "canceled") {
		t.Fatal("child failure did not cancel startup")
	}
}
