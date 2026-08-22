package engine

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRequestIDMiddleware_SetsResponseHeaderAndContext(t *testing.T) {
	var gotFromContext string
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotFromContext = requestIDFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})

	h := requestIDMiddleware()(next)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	headerID := w.Header().Get(requestIDHeader)
	if headerID == "" {
		t.Fatal("response header X-Request-Id is empty")
	}
	if gotFromContext != headerID {
		t.Errorf("requestIDFromContext() = %q, want the same id set on the response header %q", gotFromContext, headerID)
	}
}

func TestRequestIDMiddleware_MintsADifferentIDPerRequest(t *testing.T) {
	h := requestIDMiddleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	w1 := httptest.NewRecorder()
	h.ServeHTTP(w1, httptest.NewRequest(http.MethodGet, "/", nil))
	w2 := httptest.NewRecorder()
	h.ServeHTTP(w2, httptest.NewRequest(http.MethodGet, "/", nil))

	id1, id2 := w1.Header().Get(requestIDHeader), w2.Header().Get(requestIDHeader)
	if id1 == id2 {
		t.Errorf("two separate requests minted the same request id %q", id1)
	}
}

func TestRequestIDMiddleware_IgnoresInboundRequestIDHeader(t *testing.T) {
	// engine-internals.md §6 step 2 always mints a fresh id — honoring a
	// client-supplied X-Request-Id would let one client's requests spoof
	// or collide with another's in the engine's own logs.
	h := requestIDMiddleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set(requestIDHeader, "client-supplied-id")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if got := w.Header().Get(requestIDHeader); got == "client-supplied-id" {
		t.Error("middleware echoed back the client-supplied request id instead of minting its own")
	}
}

func TestRealIPMiddleware_UntrustedPeerIgnoresForwardedFor(t *testing.T) {
	h := realIPMiddleware([]string{"10.0.0.0/8"})(echoRemoteAddr())

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "203.0.113.7:54321" // not in the trusted CIDR
	req.Header.Set("X-Forwarded-For", "198.51.100.9")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if got := w.Body.String(); got != "203.0.113.7" {
		t.Errorf("resolved IP = %q, want the raw peer 203.0.113.7 (untrusted X-Forwarded-For must be ignored)", got)
	}
}

func TestRealIPMiddleware_TrustedPeerHonorsForwardedFor(t *testing.T) {
	h := realIPMiddleware([]string{"10.0.0.0/8"})(echoRemoteAddr())

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.5:54321" // inside the trusted CIDR
	req.Header.Set("X-Forwarded-For", "198.51.100.9, 10.0.0.5")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if got := w.Body.String(); got != "198.51.100.9" {
		t.Errorf("resolved IP = %q, want the first X-Forwarded-For entry 198.51.100.9", got)
	}
}

func TestRealIPMiddleware_TrustedPeerNoForwardedForFallsBackToRemoteAddr(t *testing.T) {
	h := realIPMiddleware([]string{"10.0.0.0/8"})(echoRemoteAddr())

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.5:54321"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if got := w.Body.String(); got != "10.0.0.5" {
		t.Errorf("resolved IP = %q, want the raw peer 10.0.0.5", got)
	}
}

func TestRealIPMiddleware_EmptyTrustedProxiesNeverHonorsForwardedFor(t *testing.T) {
	h := realIPMiddleware(nil)(echoRemoteAddr())

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "203.0.113.7:54321"
	req.Header.Set("X-Forwarded-For", "198.51.100.9")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if got := w.Body.String(); got != "203.0.113.7" {
		t.Errorf("resolved IP = %q, want the raw peer 203.0.113.7", got)
	}
}

func echoRemoteAddr() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(r.RemoteAddr))
	})
}
