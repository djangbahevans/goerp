package engine

import (
	"context"
	"encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"uuid"

	abi "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/auth/authcheck"
	"github.com/djangbahevans/goerp/internal/engine/httperr"
	"github.com/djangbahevans/goerp/internal/engine/registry"
	"github.com/djangbahevans/goerp/internal/engine/route"
	tenantresolve "github.com/djangbahevans/goerp/internal/engine/tenant/resolve"
	"go.opentelemetry.io/otel/trace"
)

func TestDispatchWASMApplicationErrors(t *testing.T) {
	e, mod := traceFixtureModule(t)
	mappings := map[string]int{
		"tracefixture.contact.not_found":                404,
		"tracefixture.contact.forbidden":                403,
		"tracefixture.contact.duplicate_email":          409,
		"tracefixture.contact.self_reference":           409,
		"tracefixture.contact.circular_parent":          409,
		"tracefixture.contact.already_archived":         409,
		"tracefixture.contact.invalid_state_transition": 409,
		"tracefixture.contact.invalid_email":            422,
		"tracefixture.contact.missing_name":             422,
		"tracefixture.contact.limit_exceeded":           422,
		"tracefixture.connector.unavailable":            503,
		"tracefixture.import.invalid_csv":               422,
		"tracefixture.import.row_validation_failed":     422,
		"tracefixture.contact.custom":                   418,
	}
	for code := range mappings {
		mod.ErrorCodes[code] = struct{}{}
	}

	traceID := trace.TraceID{1, 2, 3, 4}
	ctx := trace.ContextWithSpanContext(t.Context(), trace.NewSpanContext(trace.SpanContextConfig{TraceID: traceID, SpanID: trace.SpanID{1}}))
	ctx = httperr.WithRequestID(ctx, "request-1625")
	ctx = withTenantContext(ctx, &tenantresolve.TenantContext{TenantID: "00000000-0000-0000-0000-000000000001", Slug: "acme"})
	ctx = withAuthContext(ctx, &authcheck.AuthContext{IsAuthenticated: true, UserID: "00000000-0000-0000-0000-0000000000aa"})

	dispatch := func(method, path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "/tracefixture"+path, strings.NewReader(body)).WithContext(ctx)
		rr := &routeResolution{entry: &route.RouteEntry{ModuleName: "tracefixture", PathTemplate: "/tracefixture/error"}}
		w := httptest.NewRecorder()

		e.dispatchModuleRoute(ctx, w, r, rr, mod)

		return w
	}
	readError := func(t *testing.T, w *httptest.ResponseRecorder, wantStatus int, wantCode string) httperr.Body {
		t.Helper()
		var envelope httperr.Envelope
		if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
			t.Fatal(err)
		}
		if w.Code != wantStatus || envelope.Error.Code != wantCode {
			t.Fatalf("got %d %s, want %d %s", w.Code, w.Body.String(), wantStatus, wantCode)
		}
		if envelope.Error.RequestID != "request-1625" || envelope.Error.TraceID != traceID.String() {
			t.Fatalf("incorrect correlation IDs: %+v", envelope.Error)
		}

		return envelope.Error
	}

	for code, status := range mappings {
		t.Run(code, func(t *testing.T) {
			w := dispatch(http.MethodGet, "/error?code="+url.QueryEscape(code), "")
			body := readError(t, w, status, code)

			if body.Message != "private module message" || body.Details == nil {
				t.Errorf("declared error lost message or details: %+v", body)
			}
		})
	}

	for _, code := range []string{"tracefixture.contact.undeclared", "engine.route_not_found", "engine.custom.not_found", "orm.validation_failed"} {
		t.Run("undeclared "+code, func(t *testing.T) {
			w := dispatch(http.MethodGet, "/error?code="+url.QueryEscape(code), "")
			body := readError(t, w, 500, "internal_error")

			if body.Message != "An internal error occurred" || body.Details != nil || strings.Contains(w.Body.String(), "private") || strings.Contains(w.Body.String(), code) {
				t.Errorf("undeclared response leaks module data: %s", w.Body.String())
			}
			if w.Header().Get("X-Secret") != "" || w.Header().Get("Content-Length") != "" {
				t.Errorf("undeclared response leaked headers: %v", w.Header())
			}
		})
	}

	t.Run("SDK decoding failure", func(t *testing.T) {
		readError(t, dispatch(http.MethodPost, "/decode", "{"), 400, "invalid_body")
	})
	t.Run("SDK missing route", func(t *testing.T) {
		readError(t, dispatch(http.MethodGet, "/missing", ""), 404, "engine.route_not_found")
	})
	t.Run("SDK marshal failure", func(t *testing.T) {
		readError(t, dispatch(http.MethodGet, "/marshal", ""), 500, "engine.marshal_failed")
	})
}

func TestDispatchLocalizedApplicationErrors(t *testing.T) {
	name := "localized_" + strings.ReplaceAll(uuid.New().String(), "-", "")
	store := errorTranslationStore(t, name)
	e, mod := traceFixtureModule(t)
	mod.Manifest.Name = name
	code := name + ".contact.not_found"
	mod.ErrorCodes[code] = struct{}{}
	e.moduleRegistry = &registry.ModuleRegistry{Translations: store}

	for _, test := range []struct {
		locale  string
		message string
	}{
		{"fr-GH", "Contact introuvable au Ghana"},
		{"fr-CA", "Contact introuvable"},
		{"ar", "Contact not found"},
	} {
		t.Run(test.locale, func(t *testing.T) {
			e.wasmRuntime.SetLocaleResolver(func(context.Context, string, string) (abi.LocaleInfo, error) {
				return abi.LocaleInfo{Locale: test.locale, Timezone: "UTC"}, nil
			})
			ctx := withTenantContext(t.Context(), &tenantresolve.TenantContext{TenantID: "tenant", Slug: "tenant"})
			ctx = withAuthContext(ctx, &authcheck.AuthContext{IsAuthenticated: true, UserID: "user"})
			ctx = httperr.WithRequestID(ctx, "localized-request")
			r := httptest.NewRequest(http.MethodGet, "/"+name+"/not-found?code="+url.QueryEscape(code), nil).WithContext(ctx)
			w := httptest.NewRecorder()
			e.dispatchModuleRoute(ctx, w, r, &routeResolution{entry: &route.RouteEntry{ModuleName: name}}, mod)

			var got httperr.Envelope
			if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if w.Code != 404 || got.Error.Code != code || got.Error.Message != test.message || got.Error.RequestID != "localized-request" {
				t.Fatalf("response = %d %s", w.Code, w.Body.String())
			}
			if details, ok := got.Error.Details.(map[string]any); !ok || details["id"] != "c1" {
				t.Fatalf("details = %+v", got.Error.Details)
			}
		})
	}
}
