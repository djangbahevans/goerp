package abi

import (
	"bytes"
	"fmt"
	"go/build"
	"strings"
	"testing"

	"github.com/vmihailenco/msgpack/v5"
)

func decodeMap(t *testing.T, b []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := msgpack.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return m
}

func keys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func requireKeys(t *testing.T, m map[string]any, want ...string) {
	t.Helper()
	if len(m) != len(want) {
		t.Fatalf("keys = %v, want %v", keys(m), want)
	}
	for _, k := range want {
		if _, ok := m[k]; !ok {
			t.Fatalf("keys = %v, want %v", keys(m), want)
		}
	}
}

// The wire field names are the contract: editing a tag here is a
// deliberate wire change and belongs in a new package version.
func TestHostErrorWireFields(t *testing.T) {
	full, err := msgpack.Marshal(HostError{
		Code:    ErrCodeUnavailable,
		Message: "down",
		Details: map[string]any{"table": "widgets"},
		Retry:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	m := decodeMap(t, full)
	requireKeys(t, m, "code", "message", "details", "retry")
	if m["code"] != "abi.unavailable" || m["message"] != "down" || m["retry"] != true {
		t.Fatalf("decoded = %+v", m)
	}

	minimal, err := msgpack.Marshal(HostError{Code: ErrCodeNotFound, Message: "gone"})
	if err != nil {
		t.Fatal(err)
	}
	requireKeys(t, decodeMap(t, minimal), "code", "message", "retry")
}

func TestEnvelopeWireFields(t *testing.T) {
	data, err := msgpack.Marshal(map[string]any{"n": 1})
	if err != nil {
		t.Fatal(err)
	}

	ok, err := msgpack.Marshal(Envelope{OK: true, Data: data})
	if err != nil {
		t.Fatal(err)
	}
	m := decodeMap(t, ok)
	requireKeys(t, m, "ok", "data")
	if inner, isMap := m["data"].(map[string]any); !isMap || fmt.Sprint(inner["n"]) != "1" {
		t.Fatalf("data = %#v, want the embedded map value", m["data"])
	}

	failed, err := msgpack.Marshal(Envelope{Error: &HostError{Code: ErrCodeTimeout, Message: "slow"}})
	if err != nil {
		t.Fatal(err)
	}
	m = decodeMap(t, failed)
	requireKeys(t, m, "ok", "error")
	if m["ok"] != false {
		t.Fatalf("ok = %v, want false", m["ok"])
	}
}

// RawMessage must encode exactly as msgpack.RawMessage does: the bytes go
// on the wire verbatim, not wrapped as a msgpack binary value.
func TestEnvelopeMatchesMsgpackRawMessageEncoding(t *testing.T) {
	data, err := msgpack.Marshal([]int{1, 2, 3})
	if err != nil {
		t.Fatal(err)
	}
	type reference struct {
		OK    bool               `msgpack:"ok"`
		Data  msgpack.RawMessage `msgpack:"data,omitempty"`
		Error *HostError         `msgpack:"error,omitempty"`
	}

	for name, pair := range map[string][2]any{
		"success": {Envelope{OK: true, Data: data}, reference{OK: true, Data: data}},
		"failure": {
			Envelope{Error: &HostError{Code: ErrCodeMemoryFault, Message: "bad pointer"}},
			reference{Error: &HostError{Code: ErrCodeMemoryFault, Message: "bad pointer"}},
		},
	} {
		got, err := msgpack.Marshal(pair[0])
		if err != nil {
			t.Fatal(err)
		}
		want, err := msgpack.Marshal(pair[1])
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("%s: encoding differs from msgpack.RawMessage\n got %x\nwant %x", name, got, want)
		}
	}
}

func TestEnvelopeRoundTrip(t *testing.T) {
	data, err := msgpack.Marshal(map[string]string{"greeting": "hi"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := msgpack.Marshal(Envelope{OK: true, Data: data})
	if err != nil {
		t.Fatal(err)
	}

	var got Envelope
	if err := msgpack.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if !got.OK || got.Error != nil || !bytes.Equal(got.Data, data) {
		t.Fatalf("got %+v, want ok with data %x", got, data)
	}
}

func TestHostErrorMessage(t *testing.T) {
	err := &HostError{Code: ErrCodeCapabilityDenied, Message: "module did not declare db.read"}
	if got, want := err.Error(), "abi.capability_denied: module did not declare db.read"; got != want {
		t.Fatalf("Error() = %q, want %q", got, want)
	}
}

// The package must build for GOOS=wasip1, where only the standard library
// is available to a module and the engine's own dependencies are not.
func TestImportsOnlyStandardLibrary(t *testing.T) {
	pkg, err := build.ImportDir(".", 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, imp := range pkg.Imports {
		first, _, _ := strings.Cut(imp, "/")
		if strings.Contains(first, ".") {
			t.Errorf("imports %q, which is not part of the standard library", imp)
		}
	}
}

func wireKeys(t *testing.T, v any) map[string]any {
	t.Helper()
	b, err := msgpack.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return decodeMap(t, b)
}

func TestInvocationWireFields(t *testing.T) {
	tests := []struct {
		name string
		v    any
		want []string
	}{
		{"EventEnvelope", EventEnvelope{UserID: "u", TraceID: "t"}, []string{
			"id", "name", "version", "emitter_module", "tenant_id", "user_id", "trace_id", "emitted_at", "payload"}},
		{"EventEnvelope omits empty user and trace", EventEnvelope{}, []string{
			"id", "name", "version", "emitter_module", "tenant_id", "emitted_at", "payload"}},
		{"Request", Request{}, []string{
			"id", "method", "path", "params", "query", "headers", "body", "user_id", "tenant_id", "tenant_slug",
			"locale", "timezone", "currency", "direction", "trace_id", "requested_at"}},
		{"Response", Response{}, []string{"status", "headers", "body"}},
		{"ActivityRequest", ActivityRequest{}, []string{
			"activity", "payload", "tenant_id", "user_id", "trace_id", "workflow_id", "run_id", "attempt"}},
		{"ActivityResult", ActivityResult{Output: []byte("o"), Error: "e", ErrorType: "t", ErrorDetails: map[string]any{"k": 1}}, []string{
			"output", "error", "non_retryable", "error_type", "error_details"}},
		{"ActivityResult omits empty members", ActivityResult{}, []string{"non_retryable"}},
		{"MigrationJobPayload", MigrationJobPayload{}, []string{"handler", "tenant_id", "from_version", "to_version"}},
		{"RouteDeclaration", RouteDeclaration{
			RateLimit: &RateLimitDecl{}, Model: "m", Name: "n", CRUDAction: "list",
			Embedded: []EmbeddedDecl{{}}, PathParams: map[string]string{"id": "uuid"},
		}, []string{
			"method", "path", "auth", "permissions", "rate_limit", "max_body_bytes", "timeout_ms", "streaming",
			"websocket", "raw_body", "model", "name", "crud_action", "response_is_list", "embedded", "path_params"}},
		{"RouteDeclaration omits optional members", RouteDeclaration{}, []string{
			"method", "path", "auth", "permissions", "max_body_bytes", "timeout_ms", "streaming", "websocket",
			"raw_body", "response_is_list"}},
		{"RateLimitDecl", RateLimitDecl{}, []string{"requests", "window_seconds", "scope"}},
		{"EmbeddedDecl", EmbeddedDecl{}, []string{"field", "resource", "is_list"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			requireKeys(t, wireKeys(t, tt.v), tt.want...)
		})
	}
}

func TestRateLimitScopeValues(t *testing.T) {
	for scope, want := range map[RateLimitScope]string{
		RateLimitScopeUser:   "user",
		RateLimitScopeTenant: "tenant",
		RateLimitScopeIP:     "ip",
		RateLimitScopeAPIKey: "api_key",
	} {
		if string(scope) != want {
			t.Errorf("scope %q, want %q", scope, want)
		}
	}
}
