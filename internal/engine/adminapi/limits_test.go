package adminapi

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestBodyCapMiddleware_UnderLimitPasses(t *testing.T) {
	var gotBody string
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusOK)
	})

	h := bodyCapMiddleware(1024)(next)
	req := httptest.NewRequest(http.MethodPost, "/admin/tenants", strings.NewReader(`{"slug":"acme"}`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if gotBody != `{"slug":"acme"}` {
		t.Errorf("handler saw body %q, want the original body", gotBody)
	}
}

func TestBodyCapMiddleware_OverLimitRejects(t *testing.T) {
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	h := bodyCapMiddleware(4)(next)
	req := httptest.NewRequest(http.MethodPost, "/admin/tenants", strings.NewReader(`{"slug":"acme"}`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusRequestEntityTooLarge)
	}
	if called {
		t.Error("expected the wrapped handler not to run for an oversized body")
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte("body_too_large")) {
		t.Errorf("body = %s, want it to contain the body_too_large error code", rec.Body.String())
	}
}

func TestConcurrencyLimitMiddleware_RejectsBeyondMax(t *testing.T) {
	const max = 2
	release := make(chan struct{})
	entered := make(chan struct{}, max)

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		entered <- struct{}{}
		<-release
		w.WriteHeader(http.StatusOK)
	})

	h := concurrencyLimitMiddleware(max)(next)

	var wg sync.WaitGroup
	results := make(chan int, max+1)
	for range max {
		wg.Go(func() {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/admin/tenants", nil))
			results <- rec.Code
		})
	}

	// Wait for both in-flight requests to actually be inside the handler
	// before firing the one that should overflow the semaphore.
	for range max {
		<-entered
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/admin/tenants", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("overflow request status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}

	close(release)
	wg.Wait()
	close(results)
	for code := range results {
		if code != http.StatusOK {
			t.Errorf("in-flight request status = %d, want %d", code, http.StatusOK)
		}
	}
}
