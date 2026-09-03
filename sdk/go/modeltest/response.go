package modeltest

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
)

// Response is what h.GET/h.POST/etc return — an assertion-friendly
// wrapper around the real HTTP response the dispatch handler wrote
// (§7 "Asserting responses").
type Response struct {
	t          *testing.T
	StatusCode int
	Headers    map[string]string
	body       []byte
	decoded    any // lazily-parsed body, cached across JSON/JSONArray calls
	decodedOK  bool
}

func newResponse(t *testing.T, rec *httptest.ResponseRecorder) *Response {
	headers := make(map[string]string, len(rec.Header()))
	for k := range rec.Header() {
		headers[k] = rec.Header().Get(k)
	}
	return &Response{
		t:          t,
		StatusCode: rec.Code,
		Headers:    headers,
		body:       rec.Body.Bytes(),
	}
}

// ParseJSON decodes the response body into dest (a pointer), the same as
// json.Unmarshal.
func (r *Response) ParseJSON(dest any) {
	r.t.Helper()
	if err := json.Unmarshal(r.body, dest); err != nil {
		r.t.Fatalf("modeltest: parse response body as JSON: %v\nbody: %s", err, r.body)
	}
}

func (r *Response) ensureDecoded() any {
	r.t.Helper()
	if r.decodedOK {
		return r.decoded
	}
	if len(r.body) > 0 {
		if err := json.Unmarshal(r.body, &r.decoded); err != nil {
			r.t.Fatalf("modeltest: parse response body as JSON: %v\nbody: %s", err, r.body)
		}
	}
	r.decodedOK = true
	return r.decoded
}

// JSON reads a dot-separated path (e.g. "meta.total") out of the
// response body without requiring a struct.
func (r *Response) JSON(path string) any {
	r.t.Helper()
	cur := r.ensureDecoded()
	for _, seg := range strings.Split(path, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			r.t.Fatalf("modeltest: JSON(%q): %q is not an object in response body", path, seg)
		}
		cur, ok = m[seg]
		if !ok {
			return nil
		}
	}
	return cur
}

// JSONArray reads a dot-separated path to a JSON array, returning each
// element as a map[string]any (§7 "List responses").
func (r *Response) JSONArray(path string) []map[string]any {
	r.t.Helper()
	v := r.JSON(path)
	arr, ok := v.([]any)
	if !ok {
		r.t.Fatalf("modeltest: JSONArray(%q): not an array in response body", path)
	}
	out := make([]map[string]any, len(arr))
	for i, item := range arr {
		m, ok := item.(map[string]any)
		if !ok {
			r.t.Fatalf("modeltest: JSONArray(%q)[%d]: not an object", path, i)
		}
		out[i] = m
	}
	return out
}

// ErrorCode returns the response body's error.code field, per the
// {"error": {"code", "message"}} envelope every error response in this
// codebase uses (writeRouteError, sdk/go/engine's own error responses).
func (r *Response) ErrorCode() string {
	r.t.Helper()
	code := r.JSON("error.code")
	s, _ := code.(string)
	return s
}

// ValidationErrors returns the response body's error.details as a
// field-name -> messages map, for a 422 response whose details carry
// per-field validation errors (writeRouteErrorDetails' generic details
// object, keyed by field name).
func (r *Response) ValidationErrors() map[string][]string {
	r.t.Helper()
	details := r.JSON("error.details")
	m, ok := details.(map[string]any)
	if !ok {
		return nil
	}
	out := make(map[string][]string, len(m))
	for field, v := range m {
		switch vv := v.(type) {
		case string:
			out[field] = []string{vv}
		case []any:
			msgs := make([]string, len(vv))
			for i, item := range vv {
				msgs[i] = fmt.Sprint(item)
			}
			out[field] = msgs
		default:
			out[field] = []string{fmt.Sprint(vv)}
		}
	}
	return out
}
