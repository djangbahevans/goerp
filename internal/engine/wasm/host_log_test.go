package wasm

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"math"
	"strings"
	"testing"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/config"
	"github.com/djangbahevans/goerp/internal/engine/httperr"
	"github.com/rs/zerolog"
	"go.opentelemetry.io/otel/trace"
)

func newLogCaller(t *testing.T, r *Runtime) *ModuleInstance {
	t.Helper()

	compiled, err := r.wazero.CompileModule(t.Context(), buildHostCallerModule("host.log", []string{"log"}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = compiled.Close(context.Background()) })

	inst, err := newModuleInstance(t.Context(), "log-caller", compiled, r)
	if err != nil {
		t.Fatal(err)
	}
	r.RegisterInstance(inst)
	t.Cleanup(func() { r.UnregisterInstance(inst) })

	return inst
}

func TestHostLogContextAndTypes(t *testing.T) {
	r := newHostcallTestRuntime(t, nil, 10)
	var output bytes.Buffer
	r.moduleLogger = zerolog.New(&output).Level(zerolog.DebugLevel).With().Timestamp().Logger()
	inst := newLogCaller(t, r)

	span := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: trace.TraceID{1},
		SpanID:  trace.SpanID{2},
	})

	for _, invocation := range []struct {
		name, requestID, traceID string
		spanContext              bool
	}{
		{name: "request", requestID: "request-1", traceID: span.TraceID().String(), spanContext: true},
		{name: "job", traceID: "job-trace"},
		{name: "event", requestID: "event-1", traceID: "event-trace"},
		{name: "untraced"},
	} {
		t.Run(invocation.name, func(t *testing.T) {
			output.Reset()
			inst.SetModuleContext(NewModuleContext(invocation.requestID, "sales", "", "", nil, nil, "tenant-1", "", invocation.traceID, 0, nil, ModuleSnapshot{}))
			ctx := t.Context()
			if invocation.spanContext {
				ctx = trace.ContextWithSpanContext(ctx, span)
			}

			fields := map[string]any{
				"count": int64(9007199254740993), "success": true, "rate": 0.95,
				"nested": map[string]any{"items": []any{int64(3), false, "plain"}},
			}
			for _, key := range []string{"trace_id", "span_id", "tenant_id", "module", "request_id", "level", "message", "time"} {
				fields[key] = "forged"
			}
			env := callHost(t, ctx, inst, "call_log", abiv1.LogInput{Level: "info", Message: "confirmed", Fields: fields})
			if !env.OK {
				t.Fatalf("log failed: %+v", env.Error)
			}

			var got struct {
				Level     string         `json:"level"`
				Message   string         `json:"message"`
				Time      string         `json:"time"`
				TraceID   string         `json:"trace_id"`
				SpanID    string         `json:"span_id"`
				TenantID  string         `json:"tenant_id"`
				Module    string         `json:"module"`
				RequestID string         `json:"request_id"`
				Count     int64          `json:"count"`
				Success   bool           `json:"success"`
				Rate      float64        `json:"rate"`
				Nested    map[string]any `json:"nested"`
			}
			if err := json.Unmarshal(output.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			wantSpan := ""
			if invocation.spanContext {
				wantSpan = span.SpanID().String()
			}
			if got.Level != "info" || got.Message != "confirmed" || got.Time == "" || got.Time == "forged" || got.TraceID != invocation.traceID || got.SpanID != wantSpan || got.RequestID != invocation.requestID || got.TenantID != "tenant-1" || got.Module != "sales" {
				t.Fatalf("incorrect context: %+v", got)
			}
			if got.Count != 9007199254740993 || !got.Success || got.Rate != 0.95 || len(got.Nested["items"].([]any)) != 3 {
				t.Fatalf("incorrect field types: %+v", got)
			}
			if strings.Count(output.String(), "\n") != 1 {
				t.Fatalf("expected one JSON line: %s", output.String())
			}
		})
	}
}

func TestHostLogFilteringAndErrors(t *testing.T) {
	r := newHostcallTestRuntime(t, nil, 10)
	inst := newLogCaller(t, r)
	inst.SetModuleContext(NewModuleContext("", "sales", "", "", nil, nil, "tenant-1", "", "", 0, nil, ModuleSnapshot{}))

	var output bytes.Buffer
	r.moduleLogger = r.moduleLogger.Output(&output)
	if env := callHost(t, t.Context(), inst, "call_log", abiv1.LogInput{Level: "debug", Message: "hidden"}); !env.OK || output.Len() != 0 {
		t.Fatalf("debug at default level: env=%+v output=%s", env, output.String())
	}

	for _, level := range []zerolog.Level{zerolog.DebugLevel, zerolog.InfoLevel, zerolog.WarnLevel, zerolog.ErrorLevel} {
		configured, err := New(&config.Config{
			LogLevel:          level.String(),
			CompilationCache:  t.TempDir(),
			PoolMaxMemoryByes: 8 << 20,
		}, nil, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = configured.Close(context.Background()) })
		configured.moduleLogger = configured.moduleLogger.Output(&output)
		caller := newLogCaller(t, configured)
		caller.SetModuleContext(inst.ModuleContext())

		for _, requested := range []zerolog.Level{zerolog.DebugLevel, zerolog.InfoLevel, zerolog.WarnLevel, zerolog.ErrorLevel} {
			output.Reset()
			env := callHost(t, t.Context(), caller, "call_log", abiv1.LogInput{Level: requested.String(), Message: "line"})
			if !env.OK || (output.Len() > 0) != (requested >= level) {
				t.Fatalf("threshold=%s requested=%s env=%+v output=%s", level, requested, env, output.String())
			}
		}
	}

	output.Reset()
	for _, level := range []string{"", "trace", "fatal", "INFO", "invalid"} {
		env := callHost(t, t.Context(), inst, "call_log", abiv1.LogInput{Level: level})
		if env.OK || env.Error == nil || env.Error.Code != abiv1.ErrCodeLogInvalidLevel {
			t.Fatalf("invalid level %q: %+v", level, env)
		}
	}
	if output.Len() != 0 {
		t.Fatalf("invalid levels produced output: %s", output.String())
	}

	for _, input := range []any{"not a log request", abiv1.LogInput{Level: "error", Fields: map[string]any{"value": math.NaN()}}} {
		env := callHost(t, t.Context(), inst, "call_log", input)
		if env.OK || env.Error == nil || env.Error.Code != abiv1.ErrCodeDeserializeError {
			t.Fatalf("invalid payload: %+v", env)
		}
	}
	if output.Len() != 0 {
		t.Fatalf("invalid payload produced output: %s", output.String())
	}

	inst.SetModuleContext(nil)
	env := callHost(t, t.Context(), inst, "call_log", abiv1.LogInput{Level: "info"})
	if env.OK || env.Error == nil || env.Error.Code != abiv1.ErrCodeUnavailable {
		t.Fatalf("missing context: %+v", env)
	}
}

func TestHostLogSecretScrubbing(t *testing.T) {
	r := newHostcallTestRuntime(t, nil, 10)
	var output bytes.Buffer
	r.moduleLogger = r.moduleLogger.Output(&output)
	inst := newLogCaller(t, r)
	inst.SetModuleContext(NewModuleContext("req", "sales", "", "", nil, nil, "tenant", "", "", 0, nil, ModuleSnapshot{}))

	env := callHost(t, t.Context(), inst, "call_log", abiv1.LogInput{
		Level:   "info",
		Message: "failed Bearer message-token password=message-password sk_live_MESSAGE123",
		Fields: map[string]any{
			"password": "field-password", "apiKey": "field-api-key",
			"nested": map[string]any{
				"client_secret": "nested-secret",
				"items":         []any{"Bearer nested-token", map[string]any{"access_token": "array-secret"}, true, int64(2)},
			},
			"detail":     "-----BEGIN RSA PRIVATE KEY-----\nprivate-material\n-----END RSA PRIVATE KEY-----",
			"key_sample": "sk_test_FIELD123",
			"ordinary":   "order-123",
		},
	})
	if !env.OK {
		t.Fatalf("log failed: %+v", env.Error)
	}

	for _, secret := range []string{"message-token", "message-password", "MESSAGE123", "field-password", "field-api-key", "nested-secret", "nested-token", "array-secret", "private-material", "FIELD123"} {
		if strings.Contains(output.String(), secret) {
			t.Fatalf("secret %q leaked: %s", secret, output.String())
		}
	}
	var got map[string]any
	if err := json.Unmarshal(output.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got["password"] != logRedacted || got["ordinary"] != "order-123" || got["detail"] != logRedacted {
		t.Fatalf("incorrect scrubbing: %s", output.String())
	}
}

func TestHostLogUsesActiveModuleSpan(t *testing.T) {
	var output bytes.Buffer
	r := &Runtime{moduleLogger: zerolog.New(&output)}
	parent := trace.NewSpanContext(trace.SpanContextConfig{TraceID: trace.TraceID{1}, SpanID: trace.SpanID{1}})
	child := trace.NewSpanContext(trace.SpanContextConfig{TraceID: trace.TraceID{1}, SpanID: trace.SpanID{2}})
	mc := &ModuleContext{SpanStack: []trace.Span{trace.SpanFromContext(trace.ContextWithSpanContext(t.Context(), child))}}
	ctx := trace.ContextWithSpanContext(httperr.WithRequestID(t.Context(), "context-request"), parent)
	if err := r.writeModuleLog(ctx, mc, abiv1.LogInput{Level: "info"}); err != nil {
		t.Fatal(err)
	}

	var got map[string]any
	if err := json.Unmarshal(output.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got["span_id"] != child.SpanID().String() || got["trace_id"] != child.TraceID().String() || got["request_id"] != "context-request" || got["message"] != "" {
		t.Fatalf("incorrect active span: %s", output.String())
	}
}

func TestLogSecretPatterns(t *testing.T) {
	for _, value := range []string{
		"Bearer abc.def-123", "bearer token-with-dashes",
		"password=abc", `password="two words"`, `{"password":"secret"}`,
		"access_token=opaque", "api-key: opaque", "client_secret='two words'",
		"sk_live_123456", "sk_test_123456", "sk-abcdefghijklmnop",
		"AKIA1234567890ABCDEF", "ghp_abcdefghijklmnopqrstuvwxyz",
		"-----BEGIN PRIVATE KEY-----\nsecret\n-----END PRIVATE KEY-----",
		"-----BEGIN OPENSSH PRIVATE KEY-----\nsecret\n-----END OPENSSH PRIVATE KEY-----",
	} {
		t.Run(value, func(t *testing.T) {
			got := scrubLogString(value)
			if !strings.Contains(got, logRedacted) || strings.Contains(got, "secret") || strings.Contains(got, "opaque") || strings.Contains(got, "two words") {
				t.Fatalf("secret not scrubbed: %q", got)
			}
		})
	}
}
