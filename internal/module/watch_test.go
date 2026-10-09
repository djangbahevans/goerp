package module

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWatchRecoversAfterFailedBuildAndWatchesNewDirectories(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	calls := make(chan int, 10)
	done := make(chan error, 1)
	go func() {
		count := 0
		done <- Watch(ctx, dir, io.Discard, func(context.Context) error {
			count++
			calls <- count
			if count == 2 {
				return errors.New("invalid Go source")
			}
			return nil
		})
	}()
	wait := func(want int) {
		t.Helper()
		select {
		case got := <-calls:
			if got != want {
				t.Fatalf("build %d, want %d", got, want)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("watch did not rebuild")
		}
	}

	wait(1)
	path := filepath.Join(dir, "main.go")
	if err := os.WriteFile(path, []byte("broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	wait(2)
	if err := os.WriteFile(path, []byte("package main"), 0o600); err != nil {
		t.Fatal(err)
	}
	wait(3)

	nested := filepath.Join(dir, "handlers")
	if err := os.Mkdir(nested, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nested, "new.go"), []byte("package handlers"), 0o600); err != nil {
		t.Fatal(err)
	}
	wait(4)

	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestPackageFailurePreservesPreviousArchive(t *testing.T) {
	dir := t.TempDir()
	writeWasmFixture(t, dir, fixtureManifest)
	output := filepath.Join(dir, "build", "module.erp")
	if err := os.MkdirAll(filepath.Dir(output), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(output, []byte("last good package"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "module.wasm"), []byte("invalid"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "frontend", "translations"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "frontend", "translations", "en.json"), []byte(`{"bad":123}`), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := Package(t.Context(), dir, PackageOptions{Output: output, SkipWasm: true, SkipFrontend: true}); err == nil {
		t.Fatal("invalid translation was packaged")
	}
	data, err := os.ReadFile(output)
	if err != nil || string(data) != "last good package" {
		t.Fatalf("failed packaging replaced the archive: %q, %v", data, err)
	}
}

func TestPackageSidecarFailurePreservesPreviousArchive(t *testing.T) {
	dir := t.TempDir()
	output := filepath.Join(dir, "module.erp")
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(`{"name":"demo","version":"0.1.0"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(output, []byte("last good package"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(output+".sha256", 0o700); err != nil {
		t.Fatal(err)
	}

	if _, err := Package(t.Context(), dir, PackageOptions{Output: output, SkipWasm: true, SkipFrontend: true}); err == nil {
		t.Fatal("package with an unwritable sidecar succeeded")
	}

	data, err := os.ReadFile(output)
	if err != nil || string(data) != "last good package" {
		t.Fatalf("sidecar failure replaced the archive: %q, %v", data, err)
	}
}
