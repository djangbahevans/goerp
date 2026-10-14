package codegen

import (
	"bytes"
	"context"
	"encoding/json/jsontext"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func schemaWithField(field string) string {
	return `{"modules": {"contacts": {"routes": [], "views": [], "load_order": 0, "models": {
  "contacts.contact": {"enabled_ops": [], "fields": [{"name": "` + field + `", "type": "text"}]}
}}}}`
}

func TestWatchEngine_RegeneratesOnSchemaChange(t *testing.T) {
	enginePollInterval = 20 * time.Millisecond
	var schema atomic.Value
	schema.Store(schemaWithField("before"))
	var polls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		polls.Add(1)
		_, _ = w.Write([]byte(schema.Load().(string)))
	}))
	defer srv.Close()

	dir := t.TempDir()
	outPath := filepath.Join(dir, "generated.ts")
	var stdout syncBuffer
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		done <- run(ctx, &stdout, io.Discard, options{
			dir: dir, fromEngine: true, url: srv.URL, token: "k", output: outPath, watch: true, module: "contacts",
		})
	}()

	waitFor := func(want string) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if b, err := os.ReadFile(outPath); err == nil && strings.Contains(string(b), want) {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatalf("output never contained %q", want)
	}
	waitFor("before: string | null;")

	// A schema whose encoding changes but whose generated client doesn't
	// is no change.
	schema.Store(strings.Replace(schemaWithField("before"), `"routes": [], "views": []`, `"views": [], "routes": []`, 1))
	for start := polls.Load(); polls.Load() < start+3; {
		time.Sleep(5 * time.Millisecond)
	}

	schema.Store(schemaWithField("after"))
	waitFor("after: string | null;")

	cancel()
	if err := <-done; err != nil {
		t.Errorf("run() after cancel = %v, want nil", err)
	}
	if got := strings.Count(stdout.String(), "wrote "); got != 4 {
		t.Errorf("wrote %d artifacts, want 4 (two artifacts per real change); stdout:\n%s", got, stdout.String())
	}
	if strings.Contains(stdout.String(), "up to date") {
		t.Errorf("an unchanged poll reported the file; stdout:\n%s", stdout.String())
	}
}

func TestWatchEngine_RewritesFixtureWhenOnlyViewChanges(t *testing.T) {
	var schema atomic.Value
	viewSchema := func(label string) string {
		return strings.Replace(schemaWithField("name"), `"views": []`, `"views": [{"name":"custom","type":"custom","resource":"contacts.contact","label":"`+label+`"}]`, 1)
	}
	schema.Store(viewSchema("Before"))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(schema.Load().(string)))
	}))
	defer srv.Close()

	dir := t.TempDir()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		done <- run(ctx, io.Discard, io.Discard, options{
			dir:        dir,
			fromEngine: true,
			url:        srv.URL,
			token:      "k",
			output:     "client.ts",
			watch:      true,
			module:     "contacts",
		})
	}()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	})

	fixturePath := filepath.Join(dir, "client.testing.json")
	waitForFixture := func(label string) []byte {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			content, err := os.ReadFile(fixturePath)
			if err == nil && strings.Contains(string(content), `"label": "`+label+`"`) {
				return content
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatalf("fixture never contained label %q", label)
		return nil
	}
	waitForFixture("Before")
	client, err := os.ReadFile(filepath.Join(dir, "client.ts"))
	if err != nil {
		t.Fatal(err)
	}
	schema.Store(viewSchema("After"))
	fixture := waitForFixture("After")
	if !jsontext.Value(fixture).IsValid() {
		t.Fatal("fixture is not valid JSON")
	}
	again, err := os.ReadFile(filepath.Join(dir, "client.ts"))
	if err != nil || !bytes.Equal(client, again) {
		t.Fatalf("view label changed the client: %v", err)
	}
}

// syncBuffer is a bytes.Buffer safe to write from run's goroutine while
// the test reads it.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func TestTriggersLocalRun(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "frontend", "src", "api", "generated.ts")
	for path, want := range map[string]bool{
		filepath.Join(dir, "cmd", "module", "main.go"):    true,
		filepath.Join(dir, "manifest.json"):               true,
		filepath.Join(dir, "frontend", "src", "Page.tsx"): true,
		filepath.Join(dir, "frontend", "src", "util.ts"):  true,
		out: false,
		filepath.Join(dir, "frontend", "README.md"): false,
	} {
		if got := triggersLocalRun(path, out); got != want {
			t.Errorf("triggersLocalRun(%s) = %v, want %v", path, got, want)
		}
	}
}

func TestRun_RejectsRefBeforeWriting(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(schemaWithField("name")))
	}))
	defer srv.Close()

	for _, source := range []string{
		`resourceRef('contacts.contact')`,
		`resourceRef('missing.resource')`,
		`actionRef('missing.action')`,
		`permissionRef('missing:permission')`,
	} {
		t.Run(source, func(t *testing.T) {
			dir := t.TempDir()
			src := filepath.Join(dir, "frontend", "src")
			if err := os.MkdirAll(src, 0o755); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(src, "Page.tsx")
			if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
				t.Fatal(err)
			}
			out := filepath.Join(src, "generated.ts")
			if err := os.WriteFile(out, []byte("existing client"), 0o644); err != nil {
				t.Fatal(err)
			}

			err := run(t.Context(), io.Discard, io.Discard, options{
				dir: dir, fromEngine: true, url: srv.URL, token: "k", output: out, module: "contacts",
			})
			if err == nil || !strings.Contains(err.Error(), path+":1:") {
				t.Fatalf("run error = %v, want a source location", err)
			}
			if content, err := os.ReadFile(out); err != nil || string(content) != "existing client" {
				t.Fatalf("output = %q, %v, want the existing client", content, err)
			}
		})
	}
}

func TestWatchEngine_RechecksFrontendRefs(t *testing.T) {
	enginePollInterval = 20 * time.Millisecond
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(schemaWithField("name")))
	}))
	defer srv.Close()

	dir := t.TempDir()
	src := filepath.Join(dir, "frontend", "src")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(src, "Page.tsx")
	out := filepath.Join(src, "generated.ts")
	var stdout, stderr syncBuffer
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		done <- run(ctx, &stdout, &stderr, options{
			dir: dir, fromEngine: true, url: srv.URL, token: "k", output: out, watch: true, module: "contacts",
		})
	}()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("watch error: %v", err)
		}
	})

	waitFor := func(buffer *syncBuffer, want string) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if strings.Contains(buffer.String(), want) {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatalf("watch output %q lacks %q", buffer.String(), want)
	}
	waitFor(&stdout, "wrote ")
	for _, source := range []string{
		`resourceRef('contacts.contact')`,
		`actionRef('missing.action')`,
		`permissionRef('missing:permission')`,
	} {
		if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
			t.Fatal(err)
		}
		waitFor(&stderr, source[:strings.IndexByte(source, '(')]+"(")
	}
}
