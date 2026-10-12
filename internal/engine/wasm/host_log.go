package wasm

import (
	"context"
	"encoding/json/v2"
	"regexp"
	"strings"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/djangbahevans/goerp/internal/engine/httperr"
	"github.com/rs/zerolog"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/vmihailenco/msgpack/v5"
	"go.opentelemetry.io/otel/trace"
)

func registerHostLog(ctx context.Context, rt wazero.Runtime, r *Runtime) error {
	_, err := r.guardedHostModule(rt, "host.log").NewFunctionBuilder().
		WithFunc(makeLog(r)).Export("log").Instantiate(ctx)

	return err
}

func makeLog(r *Runtime) hostFunc {
	return func(ctx context.Context, m api.Module, ptr, length uint32) uint64 {
		inst := r.InstanceForModule(m)
		if inst == nil || inst.ModuleContext() == nil {
			return abi.EncodeHostError(ctx, m, m.ExportedFunction("allocate"), &abiv1.HostError{
				Code: abiv1.ErrCodeUnavailable, Message: "module execution context is not available",
			})
		}

		raw, err := abi.ReadFromModule(m.Memory(), ptr, length)
		if err != nil {
			return abi.EncodeHostError(ctx, m, inst.allocate, abi.MemoryFault())
		}

		var input abiv1.LogInput
		if err := msgpack.Unmarshal(raw, &input); err != nil {
			return abi.EncodeHostError(ctx, m, inst.allocate, abi.DeserializeError(err))
		}
		if hostErr := r.writeModuleLog(ctx, inst.ModuleContext(), input); hostErr != nil {
			return abi.EncodeHostError(ctx, m, inst.allocate, hostErr)
		}

		return abi.WriteToModule(ctx, m, inst.allocate, nil)
	}
}

func (r *Runtime) writeModuleLog(ctx context.Context, mc *ModuleContext, input abiv1.LogInput) *abiv1.HostError {
	var level zerolog.Level
	switch input.Level {
	case "debug":
		level = zerolog.DebugLevel
	case "info":
		level = zerolog.InfoLevel
	case "warn":
		level = zerolog.WarnLevel
	case "error":
		level = zerolog.ErrorLevel
	default:
		return &abiv1.HostError{Code: abiv1.ErrCodeLogInvalidLevel, Message: "log level must be debug, info, warn or error"}
	}

	event := r.moduleLogger.WithLevel(level)
	if !event.Enabled() {
		return nil
	}

	fields := make(map[string][]byte, len(input.Fields))
	for key, value := range input.Fields {
		switch key {
		case "level", "time", "message", "trace_id", "span_id", "tenant_id", "module", "request_id":
			continue
		}

		encoded, err := json.Marshal(scrubLogValue(key, value))
		if err != nil {
			event.Discard()
			return abi.DeserializeError(err)
		}
		fields[key] = encoded
	}

	spanContext := trace.SpanContextFromContext(ctx)
	if len(mc.SpanStack) > 0 {
		spanContext = mc.SpanStack[len(mc.SpanStack)-1].SpanContext()
	}

	traceID, spanID := mc.TraceID, ""
	if spanContext.HasTraceID() {
		traceID = spanContext.TraceID().String()
	}
	if spanContext.HasSpanID() {
		spanID = spanContext.SpanID().String()
	}

	requestID := mc.RequestID
	if requestID == "" {
		requestID = httperr.RequestIDFromContext(ctx)
	}

	for key, encoded := range fields {
		event.RawJSON(key, encoded)
	}
	event.Str("trace_id", traceID).Str("span_id", spanID).
		Str("tenant_id", mc.TenantID).Str("module", mc.ModuleName).
		Str("request_id", requestID).Str("message", scrubLogString(input.Message)).Send()

	return nil
}

const logRedacted = "[REDACTED]"

var (
	logSecretKey   = regexp.MustCompile(`(?i)(password|passwd|pwd|secret|token|api[_-]?key|private[_-]?key|authorization)`)
	logBearer      = regexp.MustCompile(`(?i)\bbearer\s+[a-z0-9._~+/=-]+`)
	logPrivateKey  = regexp.MustCompile(`(?s)-----BEGIN (?:[A-Z0-9]+ )*PRIVATE KEY-----.*?-----END (?:[A-Z0-9]+ )*PRIVATE KEY-----`)
	logCredentials = regexp.MustCompile(`(?i)\b(?:[a-z0-9]+[_-])*(?:password|passwd|pwd|secret|token|api[_-]?key)["']?\s*[:=]\s*(?:"[^"]*"|'[^']*'|[^\s,;]+)`)
	logAPIKey      = regexp.MustCompile(`\b(?:sk|pk)_(?:live|test)_[A-Za-z0-9]+\b|\bsk-[A-Za-z0-9_-]{16,}\b|\bAKIA[A-Z0-9]{16}\b|\bgh[pousr]_[A-Za-z0-9]{20,}\b`)
)

func scrubLogString(value string) string {
	for _, pattern := range []*regexp.Regexp{logPrivateKey, logBearer, logCredentials, logAPIKey} {
		value = pattern.ReplaceAllString(value, logRedacted)
	}
	return value
}

func scrubLogValue(key string, value any) any {
	if logSecretKey.MatchString(strings.ReplaceAll(key, " ", "")) {
		return logRedacted
	}

	switch value := value.(type) {
	case string:
		return scrubLogString(value)
	case map[string]any:
		out := make(map[string]any, len(value))
		for key, item := range value {
			out[key] = scrubLogValue(key, item)
		}
		return out
	case []any:
		out := make([]any, len(value))
		for i, item := range value {
			out[i] = scrubLogValue("", item)
		}
		return out
	default:
		return value
	}
}
