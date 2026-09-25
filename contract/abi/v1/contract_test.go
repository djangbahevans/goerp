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
		{"Request with route identity", Request{Model: "sales.order", Action: "confirm"}, []string{
			"id", "method", "path", "model", "action", "params", "query", "headers", "body", "user_id", "tenant_id", "tenant_slug",
			"locale", "timezone", "currency", "direction", "trace_id", "requested_at"}},
		{"Response", Response{}, []string{"status", "headers", "body"}},
		{"ActivityRequest", ActivityRequest{}, []string{
			"activity", "payload", "tenant_id", "user_id", "trace_id", "workflow_id", "run_id", "attempt"}},
		{"ActivityResult", ActivityResult{Output: []byte("o"), Error: "e", ErrorType: "t", ErrorDetails: map[string]any{"k": 1}}, []string{
			"output", "error", "non_retryable", "error_type", "error_details"}},
		{"ActivityResult omits empty members", ActivityResult{}, []string{"non_retryable"}},
		{"MigrationJobPayload", MigrationJobPayload{}, []string{"handler", "tenant_id", "from_version", "to_version"}},
		{"RouteDeclaration", RouteDeclaration{
			RateLimit: &RateLimitDecl{}, Model: "m", Name: "n", Scope: "record", CRUDAction: "list",
			Embedded: []EmbeddedDecl{{}}, PathParams: map[string]string{"id": "uuid"},
		}, []string{
			"method", "path", "auth", "permissions", "rate_limit", "max_body_bytes", "timeout_ms", "streaming",
			"websocket", "raw_body", "model", "name", "scope", "crud_action", "response_is_list", "embedded", "path_params"}},
		{"RouteDeclaration omits optional members", RouteDeclaration{}, []string{
			"method", "path", "auth", "permissions", "max_body_bytes", "timeout_ms", "streaming", "websocket",
			"raw_body", "response_is_list"}},
		{"RouteDeclaration with body and response types", RouteDeclaration{
			RequestType: &TypeDesc{Kind: TypeKindObject}, ResponseType: &TypeDesc{Kind: TypeKindObject},
		}, []string{
			"method", "path", "auth", "permissions", "max_body_bytes", "timeout_ms", "streaming", "websocket",
			"raw_body", "response_is_list", "request_type", "response_type"}},
		{"TypeDesc", TypeDesc{
			Kind: TypeKindObject, Name: "Contact", Elem: &TypeDesc{}, Fields: []FieldDesc{{}}, Nullable: true,
		}, []string{"kind", "name", "elem", "fields", "nullable"}},
		{"TypeDesc omits optional members", TypeDesc{Kind: TypeKindString}, []string{"kind"}},
		{"FieldDesc", FieldDesc{Name: "id", Optional: true}, []string{"name", "type", "optional"}},
		{"FieldDesc omits optional members", FieldDesc{Name: "id"}, []string{"name", "type"}},
		{"RateLimitDecl", RateLimitDecl{}, []string{"requests", "window_seconds", "scope"}},
		{"EmbeddedDecl", EmbeddedDecl{}, []string{"field", "resource", "is_list"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			requireKeys(t, wireKeys(t, tt.v), tt.want...)
		})
	}
}

func TestTypeKindValues(t *testing.T) {
	for kind, want := range map[TypeKind]string{
		TypeKindString:  "string",
		TypeKindNumber:  "number",
		TypeKindBoolean: "boolean",
		TypeKindUnknown: "unknown",
		TypeKindArray:   "array",
		TypeKindRecord:  "record",
		TypeKindObject:  "object",
	} {
		if string(kind) != want {
			t.Errorf("kind %q, want %q", kind, want)
		}
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

func TestDBWireFields(t *testing.T) {
	tests := []struct {
		name string
		v    any
		want []string
	}{
		{"DBBeginInput", DBBeginInput{}, []string{"isolation", "read_only"}},
		{"DBBeginOutput", DBBeginOutput{}, []string{"tx_id", "expires_at"}},
		{"DBTxIDInput", DBTxIDInput{}, []string{"tx_id"}},
		{"DBDurationOutput", DBDurationOutput{}, []string{"duration_ms"}},
		{"DBLockInput", DBLockInput{Shared: true}, []string{"key", "tx_id", "timeout_ms", "shared"}},
		{"DBLockOutput", DBLockOutput{}, []string{"acquired", "duration_ms"}},
		{"DBNotifyInput", DBNotifyInput{TxID: "t"}, []string{"channel", "payload", "tx_id"}},
		{"DBQueryInput", DBQueryInput{}, []string{"sql", "params", "tx_id", "opts"}},
		{"DBQueryOpts", DBQueryOpts{}, []string{"timeout_ms", "read_only"}},
		{"DBQueryOutput", DBQueryOutput{}, []string{"rows", "column_names", "rows_affected", "duration_ms"}},
		{"DBExecInput", DBExecInput{TxID: "t"}, []string{"sql", "params", "tx_id", "opts"}},
		{"DBExecOpts", DBExecOpts{TimeoutMs: 1, Returning: "id", SkipAudit: true, SkipEtag: true, ExpectRows: true},
			[]string{"timeout_ms", "returning", "skip_audit", "skip_etag", "expect_rows"}},
		{"DBExecOutput", DBExecOutput{Returning: [][]any{{1}}}, []string{"rows_affected", "returning", "duration_ms"}},
		{"DBExecBatchInput", DBExecBatchInput{TxID: "t"}, []string{"sql", "param_sets", "tx_id", "opts"}},
		{"DBExecBatchOpts", DBExecBatchOpts{TimeoutMs: 1, Returning: "id", SkipAudit: true, SkipEtag: true},
			[]string{"continue_on_error", "timeout_ms", "returning", "skip_audit", "skip_etag"}},
		{"DBExecBatchOutput", DBExecBatchOutput{Returning: [][]any{{1}}}, []string{"total_rows_affected", "returning", "duration_ms"}},
		{"DBBatchRowError", DBBatchRowError{Details: map[string]any{"k": 1}}, []string{"index", "code", "message", "details"}},
		{"DBMigrationDDLInput", DBMigrationDDLInput{Column: "c"}, []string{"op", "table", "column"}},
		{"DBMigrationDDLOutput", DBMigrationDDLOutput{}, []string{"duration_ms"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			requireKeys(t, wireKeys(t, tt.v), tt.want...)
		})
	}
}

// A request member the SDK does not set is omitted when empty, so the
// bytes the SDK sends do not depend on which members the engine accepts.
func TestDBRequestsOmitEmptyOptionalMembers(t *testing.T) {
	tests := []struct {
		name string
		v    any
		want []string
	}{
		{"DBLockInput", DBLockInput{}, []string{"key", "tx_id", "timeout_ms"}},
		{"DBNotifyInput", DBNotifyInput{}, []string{"channel", "payload"}},
		{"DBExecInput", DBExecInput{}, []string{"sql", "params", "opts"}},
		{"DBExecOpts", DBExecOpts{}, nil},
		{"DBExecBatchInput", DBExecBatchInput{}, []string{"sql", "param_sets", "opts"}},
		{"DBExecBatchOpts", DBExecBatchOpts{}, []string{"continue_on_error"}},
		{"DBMigrationDDLInput", DBMigrationDDLInput{}, []string{"op", "table"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			requireKeys(t, wireKeys(t, tt.v), tt.want...)
		})
	}
}

func TestORMWireFields(t *testing.T) {
	etag := ""
	tests := []struct {
		name string
		v    any
		want []string
	}{
		{"ORMSearchInput", ORMSearchInput{Order: "o", Limit: 1, Offset: 1}, []string{"model", "domain", "order", "limit", "offset", "tx_id"}},
		{"ORMSearchOutput", ORMSearchOutput{}, []string{"ids", "count"}},
		{"ORMSearchReadInput", ORMSearchReadInput{Fields: []string{"a"}, Order: "o", Limit: 1, Offset: 1, Cursor: "c"},
			[]string{"model", "domain", "fields", "order", "limit", "offset", "cursor", "tx_id"}},
		{"ORMSearchReadOutput", ORMSearchReadOutput{NextCursor: "c"}, []string{"records", "next_cursor"}},
		{"ORMReadInput", ORMReadInput{Fields: []string{"a"}}, []string{"model", "ids", "fields", "tx_id"}},
		{"ORMReadOutput", ORMReadOutput{}, []string{"records"}},
		{"ORMOnConflict", ORMOnConflict{}, []string{"fields", "policy"}},
		{"ORMCreateInput", ORMCreateInput{OnConflict: &ORMOnConflict{}}, []string{"model", "record", "on_conflict", "tx_id"}},
		{"ORMCreateOutput", ORMCreateOutput{}, []string{"record"}},
		{"ORMCreateBatchInput", ORMCreateBatchInput{OnConflict: &ORMOnConflict{}}, []string{"model", "records", "on_conflict", "tx_id"}},
		{"ORMCreateBatchOutput", ORMCreateBatchOutput{}, []string{"records"}},
		{"ORMFirstOrCreateInput", ORMFirstOrCreateInput{}, []string{"model", "unique_vals", "create_vals", "tx_id"}},
		{"ORMFirstOrCreateOutput", ORMFirstOrCreateOutput{}, []string{"record", "created"}},
		{"ORMWriteInput", ORMWriteInput{ExpectedEtag: &etag}, []string{"model", "id", "record", "expected_etag", "tx_id"}},
		{"ORMWriteOutput", ORMWriteOutput{}, []string{"record"}},
		{"ORMWriteManyInput", ORMWriteManyInput{}, []string{"model", "ids", "record", "tx_id"}},
		{"ORMWriteWhereInput", ORMWriteWhereInput{}, []string{"model", "domain", "record", "tx_id"}},
		{"ORMMutateOp", ORMMutateOp{}, []string{"field", "delta"}},
		{"ORMMutateInput", ORMMutateInput{Guard: "g"}, []string{"model", "id", "ops", "guard", "tx_id"}},
		{"ORMMutateOutput", ORMMutateOutput{}, []string{"record"}},
		{"ORMAggregateValue", ORMAggregateValue{Field: "f", Aggregation: "sum"}, []string{"field", "aggregation"}},
		{"ORMAggregateInput", ORMAggregateInput{Domain: "d"}, []string{"model", "domain", "values", "tx_id"}},
		{"ORMAggregateOutput", ORMAggregateOutput{}, []string{"values"}},
		{"ORMExecResult", ORMExecResult{}, []string{"count", "ids"}},
		{"ORMUnlinkInput", ORMUnlinkInput{}, []string{"model", "ids", "tx_id"}},
		{"ORMRecordCreatedPayload (single)", ORMRecordCreatedPayload{Record: map[string]any{"id": "x"}}, []string{"model", "record"}},
		{"ORMRecordCreatedPayload (batch)", ORMRecordCreatedPayload{Records: []map[string]any{{"id": "x"}}}, []string{"model", "records"}},
		{"ORMRecordUpdatedPayload", ORMRecordUpdatedPayload{}, []string{"model", "record", "changed_fields"}},
		{"ORMRecordDeletedPayload", ORMRecordDeletedPayload{}, []string{"model", "record"}},
		{"ComputeRequest", ComputeRequest{TenantID: "t", UserID: "u", TraceID: "r"}, []string{"fn_name", "record", "tenant_id", "user_id", "trace_id"}},
		{"ComputeResponse", ComputeResponse{Value: 1, Error: &ComputeError{}}, []string{"value", "error"}},
		{"ComputeError", ComputeError{}, []string{"code", "message"}},
		{"ConstraintRequest", ConstraintRequest{TenantID: "t", UserID: "u", TraceID: "r"}, []string{"model", "phase", "record", "tenant_id", "user_id", "trace_id"}},
		{"ConstraintResponse", ConstraintResponse{Field: "f", Message: "m", Error: &ConstraintError{}}, []string{"allowed", "field", "message", "error"}},
		{"ConstraintError", ConstraintError{}, []string{"code", "message"}},
		{"PreviewRequest", PreviewRequest{TenantID: "t", UserID: "u", TraceID: "r"}, []string{"model", "record", "tenant_id", "user_id", "trace_id"}},
		{"PreviewResponse", PreviewResponse{Record: map[string]any{"a": 1}, Error: &PreviewError{}}, []string{"record", "error"}},
		{"PreviewError", PreviewError{}, []string{"code", "message"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			requireKeys(t, wireKeys(t, tt.v), tt.want...)
		})
	}
}

func TestORMRequestsOmitEmptyOptionalMembers(t *testing.T) {
	tests := []struct {
		name string
		v    any
		want []string
	}{
		{"ORMSearchInput", ORMSearchInput{}, []string{"model", "domain", "tx_id"}},
		{"ORMSearchReadInput", ORMSearchReadInput{}, []string{"model", "domain", "tx_id"}},
		{"ORMCreateInput", ORMCreateInput{}, []string{"model", "record", "tx_id"}},
		{"ORMWriteInput", ORMWriteInput{}, []string{"model", "id", "record", "tx_id"}},
		{"ComputeRequest", ComputeRequest{}, []string{"fn_name", "record"}},
		{"ComputeResponse", ComputeResponse{}, nil},
		{"ConstraintResponse", ConstraintResponse{}, []string{"allowed"}},
		{"PreviewResponse", PreviewResponse{}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			requireKeys(t, wireKeys(t, tt.v), tt.want...)
		})
	}
}

func TestStorageSearchAuthzEventWireFields(t *testing.T) {
	tests := []struct {
		name string
		v    any
		want []string
	}{
		{"StorageUploadOpts", StorageUploadOpts{}, []string{"public", "max_size_bytes", "purpose"}},
		{"StorageUploadInput", StorageUploadInput{}, []string{"filename", "content_type", "data", "opts"}},
		{"StorageUploadOutput", StorageUploadOutput{URL: "u"}, []string{"file_id", "storage_key", "size_bytes", "checksum_sha256", "url"}},
		{"StorageUploadOutput omits empty url", StorageUploadOutput{}, []string{"file_id", "storage_key", "size_bytes", "checksum_sha256"}},
		{"SearchQueryOpts", SearchQueryOpts{Filter: "f", Sort: []string{"s"}, Limit: 1, Offset: 1, Facets: []string{"x"}},
			[]string{"filter", "sort", "limit", "offset", "facets"}},
		{"SearchQueryOpts omits empty members", SearchQueryOpts{}, nil},
		{"SearchQueryInput", SearchQueryInput{}, []string{"index", "query", "opts"}},
		{"SearchQueryOutput", SearchQueryOutput{FacetDistribution: map[string]map[string]int{"a": {"b": 1}}},
			[]string{"hits", "total_hits", "processing_time_ms", "facet_distribution"}},
		{"AuthzFieldCheckInput", AuthzFieldCheckInput{}, []string{"user_id", "model", "field", "kind"}},
		{"AuthzFieldCheckOutput", AuthzFieldCheckOutput{}, []string{"allowed"}},
		{"EventEmitTxInput", EventEmitTxInput{DelayMs: 1, IdempotencyKey: "k", Sync: true},
			[]string{"tx_id", "name", "version", "payload", "delay_ms", "idempotency_key", "sync"}},
		{"EventEmitTxInput omits empty members", EventEmitTxInput{}, []string{"tx_id", "name", "version", "payload"}},
		{"EventEmitInput", EventEmitInput{DelayMs: 1, IdempotencyKey: "k", Sync: true},
			[]string{"name", "version", "payload", "delay_ms", "idempotency_key", "sync"}},
		{"EventEmitTxOutput", EventEmitTxOutput{}, []string{"event_id"}},
		{"EventEmitOutput", EventEmitOutput{}, []string{"event_id"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			requireKeys(t, wireKeys(t, tt.v), tt.want...)
		})
	}
}

// The kind is encoded as its integer value, which the engine and the SDK
// share.
func TestAuthzFieldCheckKindValues(t *testing.T) {
	if AuthzFieldCheckRead != 0 || AuthzFieldCheckWrite != 1 {
		t.Fatalf("read=%d write=%d, want 0 and 1", AuthzFieldCheckRead, AuthzFieldCheckWrite)
	}
}

func TestVirtualOpWireFields(t *testing.T) {
	tests := []struct {
		name string
		v    any
		want []string
	}{
		{"VirtualOpRequest", VirtualOpRequest{ID: "i", Record: map[string]any{"a": 1}, ExpectedEtag: "e", Limit: 1, Offset: 1, TenantID: "t", UserID: "u", TraceID: "r"},
			[]string{"model", "op", "id", "record", "expected_etag", "limit", "offset", "tenant_id", "user_id", "trace_id"}},
		{"VirtualOpRequest omits empty members", VirtualOpRequest{}, []string{"model", "op"}},
		{"VirtualOpResponse", VirtualOpResponse{Record: map[string]any{"a": 1}, Records: []map[string]any{{"a": 1}}, Error: &VirtualOpError{}},
			[]string{"record", "records", "error"}},
		{"VirtualOpResponse omits empty members", VirtualOpResponse{}, nil},
		{"VirtualOpError", VirtualOpError{}, []string{"code", "message"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			requireKeys(t, wireKeys(t, tt.v), tt.want...)
		})
	}
}
