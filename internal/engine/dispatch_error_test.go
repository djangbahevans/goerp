package engine

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	abi "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/httperr"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/wasm"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

func TestDispatchHandler_InternalFailuresAreRedactedAndLogged(t *testing.T) {
	for _, tc := range []struct {
		name   string
		locale bool
		detail string
	}{
		{name: "locale", locale: true, detail: "private database credentials"},
		{name: "WASM trap", detail: "unreachable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newTestEngine(t)
			if tc.locale {
				e.wasmRuntime.SetLocaleResolver(func(context.Context, string, string) (abi.LocaleInfo, error) {
					return abi.LocaleInfo{}, errors.New(tc.detail)
				})
			}
			pool := newTestWASMPool(t, handleRequestTrapsModule, wasm.PoolConfig{MaxSize: 1, WarmSize: 1})
			mods := map[string]*module.LoadedModule{
				"widgets": {
					Status:   module.StatusReady,
					Manifest: manifest.Manifest{Name: "widgets", Type: "standard"},
					Pool:     pool,
				},
			}

			var logs bytes.Buffer
			previous := log.Logger
			log.Logger = zerolog.New(&logs)
			t.Cleanup(func() { log.Logger = previous })

			w := dispatchTestModule(t, e, mods, 0)
			var envelope httperr.Envelope
			if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
				t.Fatal(err)
			}
			if w.Code != http.StatusInternalServerError || envelope.Error.Code != "internal_error" || envelope.Error.Message != "An internal error occurred" || envelope.Error.Details != nil || envelope.Error.RequestID != "dispatch-request" {
				t.Fatalf("internal response = %d: %s", w.Code, w.Body.String())
			}
			if strings.Contains(w.Body.String(), tc.detail) {
				t.Fatalf("internal detail leaked: %s", w.Body.String())
			}

			var event struct {
				Error     string `json:"error"`
				RequestID string `json:"request_id"`
			}
			if err := json.Unmarshal(logs.Bytes(), &event); err != nil {
				t.Fatalf("decode log: %v; log: %s", err, logs.String())
			}
			if !strings.Contains(event.Error, tc.detail) || event.RequestID != "dispatch-request" {
				t.Fatalf("missing internal detail or correlation ID: %+v", event)
			}
		})
	}
}

func TestWriteDispatchError_HandlerDeadline(t *testing.T) {
	ctx, cancel := context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
	defer cancel()

	for _, timeout := range []time.Duration{0, 50 * time.Millisecond} {
		w := httptest.NewRecorder()
		writeDispatchError(ctx, w, errors.New("private handler failure"), timeout)

		if w.Code != http.StatusServiceUnavailable {
			t.Fatalf("status = %d: %s", w.Code, w.Body.String())
		}
		if timeout == 0 {
			timeout = defaultHandlerTimeout
		}
		if !strings.Contains(w.Body.String(), timeout.String()) || strings.Contains(w.Body.String(), "private") {
			t.Fatalf("timeout response = %s", w.Body.String())
		}
		assertRouteErrorCode(t, w, "computation_limit_exceeded")
	}
}

func TestDispatchHandler_WASMHandlerTimeout(t *testing.T) {
	// Replace the final trap body with an infinite loop and adjust the code-section
	// length. The route deadline must interrupt guest execution through wazero.
	guest := bytes.Clone(handleRequestTrapsModule[:len(handleRequestTrapsModule)-4])
	guest[len(handleRequestTrapsModule)-27] = 0x1f
	guest = append(guest, 0x08, 0x00, 0x03, 0x40, 0x0c, 0x00, 0x0b, 0x00, 0x0b)

	e := newTestEngine(t)
	pool := newTestWASMPool(t, guest, wasm.PoolConfig{MaxSize: 1, WarmSize: 1})
	mods := map[string]*module.LoadedModule{
		"widgets": {
			Status:   module.StatusReady,
			Manifest: manifest.Manifest{Name: "widgets", Type: "standard"},
			Pool:     pool,
		},
	}
	w := dispatchTestModule(t, e, mods, 50*time.Millisecond)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("handler timeout = %d: %s", w.Code, w.Body.String())
	}
	assertRouteErrorCode(t, w, "computation_limit_exceeded")
}
