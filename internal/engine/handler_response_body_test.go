package engine

import (
	"testing"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
)

func TestHandlerResponseBody(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{"valid JSON is sent as-is", `{"id":"c1","email":null}`, `{"id":"c1","email":null}`},
		{"JSON is compacted and escaped", `{ "html": "<b>" }`, `{"html":"\u003cb\u003e"}`},
		{"no body sends no body", ``, ``},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := handlerResponseBody(abiv1.Response{Body: []byte(tt.body)})
			if err != nil {
				t.Fatalf("handlerResponseBody() error = %v", err)
			}
			if string(got) != tt.want {
				t.Errorf("handlerResponseBody() = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestHandlerResponseBody_RejectsInvalidJSON(t *testing.T) {
	for _, body := range []string{`{"id":`, `{"a":1,"a":2}`, "{\"s\":\"\xff\"}"} {
		if _, err := handlerResponseBody(abiv1.Response{Body: []byte(body)}); err == nil {
			t.Errorf("handlerResponseBody(%q) error = nil, want an error", body)
		}
	}
}
