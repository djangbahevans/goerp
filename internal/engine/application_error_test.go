package engine

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"uuid"

	abi "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/httperr"
	"github.com/djangbahevans/goerp/internal/engine/l10n/translationstore"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/registry"
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
			resp := (&Engine{}).moduleResponse(wire, []byte(tt.body), mod, "fr")
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

func errorTranslationStore(t *testing.T, name string) *translationstore.Store {
	t.Helper()
	conn := openDispatchORMTestDB(t)
	store := translationstore.New(conn)
	if err := store.Bootstrap(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := conn.Exec("DELETE FROM system.module_translations WHERE module_name=$1", name); err != nil {
			t.Error(err)
		}
	})
	if err := store.Apply(t.Context(), map[string]map[string]map[string]string{
		name: {
			"en": {
				"errors.contact.not_found": "Contact not found",
				"errors.contact.custom":    "Custom error",
			},
			"fr":    {"errors.contact.not_found": "Contact introuvable"},
			"fr-GH": {"errors.contact.not_found": "Contact introuvable au Ghana"},
			"he":    {"errors.contact.not_found": "איש קשר לא נמצא"},
			"de":    {"errors.contact.not_found": ""},
			"es":    {"errors.contact.not_found": "errors.contact.not_found"},
			"pt":    {"errors.contact.not_found": "<Contato> & \"nome\""},
			"it":    {"errors.contact.not_found": "Missing {id}"},
		},
	}, nil); err != nil {
		t.Fatal(err)
	}
	return store
}

func TestModuleApplicationResponseTranslations(t *testing.T) {
	name := "localized_" + strings.ReplaceAll(uuid.New().String(), "-", "")
	store := errorTranslationStore(t, name)
	e := &Engine{moduleRegistry: &registry.ModuleRegistry{Translations: store}}
	mod := &module.LoadedModule{
		Manifest: manifest.Manifest{Name: name},
		ErrorCodes: map[string]struct{}{
			name + ".contact.not_found": {},
			name + ".contact.custom":    {},
			name + ".contact.missing":   {},
		},
	}
	for _, test := range []struct {
		name       string
		locale     string
		suffix     string
		status     int
		wantStatus int
		message    string
		translated bool
	}{
		{"exact", "fr-GH", "contact.not_found", 404, 404, "Contact introuvable au Ghana", true},
		{"language", "fr-CA", "contact.not_found", 404, 404, "Contact introuvable", true},
		{"english", "ar", "contact.not_found", 404, 404, "Contact not found", true},
		{"missing", "fr", "contact.missing", 418, 418, "Handler message", false},
		{"empty", "de", "contact.not_found", 404, 404, "", true},
		{"equal to key", "es", "contact.not_found", 404, 404, "errors.contact.not_found", true},
		{"escaping", "pt", "contact.not_found", 404, 404, "<Contato> & \"nome\"", true},
		{"details are not parameters", "it", "contact.not_found", 404, 404, "Missing {id}", true},
		{"custom status", "fr", "contact.custom", 418, 418, "Custom error", true},
		{"mapped status", "fr", "contact.not_found", 500, 404, "Contact introuvable", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			code := name + "." + test.suffix
			body := []byte(`{"extra":{"keep":true},"error":{"code":"` + code + `","message":"Handler message","details":{"id":9007199254740993,"nested":[null,true,"{name}"]},"hint":"keep"}}`)
			wire := abi.Response{StatusCode: test.status, Headers: map[string]string{"content-length": "999", "X-Module": "keep"}}
			resp := e.moduleResponse(wire, body, mod, test.locale)
			var got, original map[string]any
			if err := json.Unmarshal(resp.Body, &got); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(body, &original); err != nil {
				t.Fatal(err)
			}
			original["error"].(map[string]any)["message"] = test.message
			if !reflect.DeepEqual(got, original) || resp.StatusCode != test.wantStatus {
				t.Fatalf("response = %d %s; want %d %+v", resp.StatusCode, resp.Body, test.wantStatus, original)
			}
			var raw struct {
				Error struct {
					Details jsontext.Value `json:"details"`
				} `json:"error"`
			}
			if err := json.Unmarshal(resp.Body, &raw); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(raw.Error.Details), "9007199254740993") {
				t.Fatalf("details lost integer precision: %s", raw.Error.Details)
			}
			if test.translated && resp.Headers["content-length"] != "" {
				t.Fatal("translated body retains stale Content-Length")
			}
			if resp.Headers["X-Module"] != "keep" || wire.Headers["content-length"] != "999" {
				t.Fatalf("headers changed: response %v, wire %v", resp.Headers, wire.Headers)
			}
			if !test.translated && string(resp.Body) != string(body) {
				t.Fatal("missing translation changed the body")
			}
		})
	}

	t.Run("framework error", func(t *testing.T) {
		body := []byte(`{"error":{"code":"engine.route_not_found","message":"no route matched"}}`)
		resp := e.moduleResponse(abi.Response{StatusCode: 404, ErrorKind: abi.FrameworkError}, body, mod, "fr")
		if resp.StatusCode != 404 || string(resp.Body) != string(body) {
			t.Fatalf("framework response changed: %d %s", resp.StatusCode, resp.Body)
		}
	})
	t.Run("module without translations", func(t *testing.T) {
		otherName := name + "_other"
		code := otherName + ".contact.not_found"
		otherMod := &module.LoadedModule{
			Manifest:   manifest.Manifest{Name: otherName},
			ErrorCodes: map[string]struct{}{code: {}},
		}
		body := []byte(`{"error":{"code":"` + code + `","message":"handler message"}}`)
		resp := e.moduleResponse(abi.Response{StatusCode: 404}, body, otherMod, "fr")
		if resp.StatusCode != 404 || string(resp.Body) != string(body) {
			t.Fatalf("module without translations changed: %d %s", resp.StatusCode, resp.Body)
		}
	})
}
