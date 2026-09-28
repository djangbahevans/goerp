package codegen

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
	ctx, cancel := context.WithCancel(context.Background())
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
	if got := strings.Count(stdout.String(), "wrote "); got != 2 {
		t.Errorf("wrote the client %d times, want 2 (once per real change); stdout:\n%s", got, stdout.String())
	}
	if strings.Contains(stdout.String(), "up to date") {
		t.Errorf("an unchanged poll reported the file; stdout:\n%s", stdout.String())
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
