package engine

import (
	"encoding/json/v2"
	"net/http/httptest"
	"strings"
	"testing"

	abi "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/httperr"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/module"
)

func TestModuleApplicationResponse(t *testing.T) {
	mod := &module.LoadedModule{
		Manifest: manifest.Manifest{Name: "contacts"},
		ErrorCodes: map[string]struct{}{
			"contacts.contact.not_found": {},
			"contacts.contact.custom":    {},
		},
	}
	type testCase struct {
		name   string
		status int
		kind   string
		body   string
		want   int
		code   string
	}
	tests := []testCase{
		{"declared mapping", 500, "", `{"error":{"code":"contacts.contact.not_found","message":"missing","details":{"id":"c1"}}}`, 404, "contacts.contact.not_found"},
		{"custom fallback", 403, "", `{"error":{"code":"contacts.contact.custom","message":"custom"}}`, 403, "contacts.contact.custom"},
		{"custom success status", 200, "", `{"error":{"code":"contacts.contact.custom"}}`, 500, "contacts.contact.custom"},
		{"undeclared", 409, "", `{"error":{"code":"contacts.contact.secret","message":"secret","details":{"password":"secret"}},"extra":"secret"}`, 500, "internal_error"},
		{"engine prefix", 404, "", `{"error":{"code":"engine.route_not_found","message":"secret"}}`, 500, "internal_error"},
		{"forged classification", 404, abi.FrameworkError, `{"error":{"code":"engine.secret.not_found","message":"secret"}}`, 500, "internal_error"},
		{"unknown framework zero status", 0, abi.FrameworkError, `{"error":{"code":"engine.secret.not_found","message":"secret"}}`, 500, "internal_error"},
		{"wrong framework status", 409, abi.FrameworkError, `{"error":{"code":"invalid_body","message":"secret"}}`, 500, "internal_error"},
		{"classified application", 404, abi.FrameworkError, `{"error":{"code":"contacts.contact.not_found","message":"secret"}}`, 500, "internal_error"},
		{"unknown classification", 404, "custom", `{"error":{"code":"contacts.contact.not_found","message":"secret"}}`, 500, "internal_error"},
		{"missing code", 400, "", `{"error":{"message":"secret"}}`, 500, "internal_error"},
		{"nonstring code", 400, "", `{"error":{"code":123,"message":"secret"}}`, 500, "internal_error"},
		{"null error", 400, "", `{"error":null,"secret":true}`, 500, "internal_error"},
		{"unstructured error", 400, "", `{"secret":true}`, 500, "internal_error"},
		{"empty error", 400, "", "", 500, "internal_error"},
		{"success envelope bypass", 200, "", `{"error":{"code":"contacts.contact.secret","message":"secret"}}`, 500, "internal_error"},
		{"duplicate error success", 200, "", `{"error":{"code":"contacts.contact.secret"},"error":null}`, 500, "internal_error"},
		{"success", 200, "", `{"data":"ok"}`, 200, ""},
		{"array success", 200, "", `[{"id":"c1"}]`, 200, ""},
		{"scalar success", 200, "", `"ok"`, 200, ""},
		{"null success", 200, "", `null`, 200, ""},
		{"no content", 204, "", "", 204, ""},
	}

	for code, status := range map[string]int{
		"invalid_body":           400,
		"engine.invalid_request": 400,
		"engine.route_not_found": 404,
		"engine.marshal_failed":  500,
		"orm.validation_failed":  422,
	} {
		tests = append(tests, testCase{code, status, abi.FrameworkError, `{"error":{"code":"` + code + `","message":"framework"}}`, status, code})
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wire := abi.Response{StatusCode: tt.status, ErrorKind: tt.kind, Headers: map[string]string{"X-Module": "secret", "Content-Length": "999"}}
			resp := moduleResponse(wire, []byte(tt.body), mod)
			ctx := httperr.WithRequestID(t.Context(), "req-1625")
			w := httptest.NewRecorder()

			writeResponse(ctx, w, resp)

			if w.Code != tt.want {
				t.Fatalf("status = %d, want %d: %s", w.Code, tt.want, w.Body.String())
			}
			if tt.code == "" {
				if w.Body.String() != tt.body {
					t.Errorf("body = %q, want %q", w.Body.String(), tt.body)
				}

				return
			}

			var envelope httperr.Envelope
			if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
				t.Fatal(err)
			}
			if envelope.Error.Code != tt.code || envelope.Error.RequestID != "req-1625" {
				t.Errorf("envelope = %+v", envelope)
			}
			if tt.code == "internal_error" {
				if strings.Contains(w.Body.String(), "secret") || envelope.Error.Details != nil || w.Header().Get("X-Module") != "" {
					t.Errorf("module data leaked: headers = %v, body = %s", w.Header(), w.Body.String())
				}
			}
		})
	}
}
