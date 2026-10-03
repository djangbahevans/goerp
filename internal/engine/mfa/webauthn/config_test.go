package webauthn

import (
	"errors"
	"testing"
)

func TestForRequestRelyingPartyAndOrigin(t *testing.T) {
	tests := []struct {
		name     string
		cfg      Config
		host     string
		origin   string
		wantRPID string
	}{
		{"platform subdomain", Config{RPID: "goerp.test", BaseURL: "https://app.goerp.test/"}, "acme.goerp.test:8080", "https://acme.goerp.test", "goerp.test"},
		{"case-insensitive host", Config{RPID: "goerp.test", BaseURL: "https://app.goerp.test"}, "ACME.GOERP.TEST", "https://acme.goerp.test", "goerp.test"},
		{"custom domain", Config{RPID: "goerp.test", BaseURL: "https://app.goerp.test"}, "erp.example.com", "https://erp.example.com", "erp.example.com"},
		{"localhost proxy port", Config{RPID: "localhost", BaseURL: "http://localhost:5173"}, "acme.localhost:8080", "http://acme.localhost:5173", "localhost"},
		{"explicit port", Config{RPID: "goerp.test", RPOrigins: []string{"https://erp.example.com:8443"}}, "erp.example.com", "https://erp.example.com:8443", "erp.example.com"},
		{"unlisted origin", Config{RPID: "goerp.test", RPOrigins: []string{"https://acme.goerp.test"}}, "other.goerp.test", "https://other.goerp.test", ""},
		{"wrong host", Config{RPID: "goerp.test", BaseURL: "https://app.goerp.test"}, "acme.goerp.test", "https://evil.goerp.test", ""},
		{"wrong port", Config{RPID: "goerp.test", BaseURL: "https://app.goerp.test"}, "acme.goerp.test", "https://acme.goerp.test:8443", ""},
		{"plaintext custom domain", Config{RPID: "goerp.test", BaseURL: "https://app.goerp.test"}, "erp.example.com", "http://erp.example.com", ""},
		{"missing origin", Config{RPID: "goerp.test", BaseURL: "https://app.goerp.test"}, "acme.goerp.test", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, err := NewService(tt.cfg, nil, nil, nil, nil)
			if err != nil {
				t.Fatal(err)
			}

			bound, err := svc.ForRequest(tt.host, tt.origin, "session:test")
			if tt.wantRPID == "" {
				if !errors.Is(err, ErrInvalidOrigin) {
					t.Fatalf("error = %v, want invalid origin", err)
				}

				return
			}

			if err != nil {
				t.Fatal(err)
			}

			if bound.webAuthn.Config.RPID != tt.wantRPID || bound.webAuthn.Config.RPOrigins[0] != tt.origin || bound.binding != "session:test" {
				t.Fatalf("bound relying party = %+v, binding = %q", bound.webAuthn.Config, bound.binding)
			}
		})
	}
}

func TestNewServiceRejectsInvalidConfig(t *testing.T) {
	for _, cfg := range []Config{
		{RPID: "", RPOrigins: []string{"https://goerp.test"}},
		{RPID: "https://goerp.test", RPOrigins: []string{"https://goerp.test"}},
		{RPID: "goerp.test", RPOrigins: []string{"https://*.goerp.test"}},
		{RPID: "goerp.test", RPOrigins: []string{"http://goerp.test"}},
		{RPID: "goerp.test", RPOrigins: []string{"https://goerp.test/path"}},
		{RPID: "goerp.test", RPOrigins: []string{"https://user@goerp.test"}},
	} {
		if _, err := NewService(cfg, nil, nil, nil, nil); err == nil {
			t.Fatalf("accepted invalid configuration: %+v", cfg)
		}
	}
}
